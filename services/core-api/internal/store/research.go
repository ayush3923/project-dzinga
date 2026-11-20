package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Research requests: Claude's work queue. Web research (finding a job posting) and
// Opus fit scores are done by Claude in the Claude app, not through the API.
// Company profiles moved to the organizations service with its own queue (ADR 0010). The web app or a refresh queues a request; a scheduled
// Claude task works through the queue (see docs/runbooks/claude-queue.md), and
// saving the result marks the request done. The daily quiz lives in the quiz
// service (ADR 0008).
//
// Targets are applications (job_posting, fit_score, interview_review, outreach).
// Interview reviews are queued by saving a note (interview.go) and cold emails by
// RequestOutreach, not through RequestResearch.

var ErrBadKind = errors.New("kind must be job_posting or fit_score")

// appKinds are the request kinds whose target is an application.
const appKinds = `('job_posting', 'fit_score', 'interview_review', 'outreach')`

type ResearchRequest struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	TargetID  int64     `json:"target_id"`
	Name      string    `json:"name"`    // organization name, or "Company — Role"
	JobURL    string    `json:"job_url"` // job_posting: the link already known, if any
	CreatedAt time.Time `json:"created_at"`
	// outreach: who the email is for, a contact on the application or as the user typed it.
	ContactID *int64 `json:"contact_id,omitempty"`
	Recipient string `json:"recipient,omitempty"`
}

// RequestResearch queues a request (once: a pending one is returned as is).
func (s *Store) RequestResearch(ctx context.Context, userID, kind string, targetID int64, now time.Time) (time.Time, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	switch kind {
	case "job_posting", "fit_score":
		err = ownsApp(ctx, tx, userID, targetID)
	default:
		return time.Time{}, ErrBadKind
	}
	if err != nil {
		return time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO research_requests (user_id, kind, target_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, userID, kind, targetID, now.UTC().Format(time.RFC3339)); err != nil {
		return time.Time{}, err
	}
	var created string
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM research_requests WHERE user_id = ? AND kind = ? AND target_id = ? AND status = 'pending'`,
		userID, kind, targetID).Scan(&created); err != nil {
		return time.Time{}, err
	}
	at, _ := time.Parse(time.RFC3339, created)
	return at, tx.Commit()
}

// PendingResearch lists the queue, oldest first.
func (s *Store) PendingResearch(ctx context.Context, userID string) ([]ResearchRequest, error) {
	out := []ResearchRequest{}
	err := s.scanRows(ctx, `SELECT r.id, r.kind, r.target_id, r.created_at,
			a.company || CASE WHEN a.role <> '' THEN ' — ' || a.role ELSE '' END, COALESCE(a.job_url, ''), r.contact_id, r.recipient
		FROM research_requests r
		JOIN applications a ON a.id = r.target_id -- not for a since-merged application
		WHERE r.user_id = ? AND r.status = 'pending' AND r.kind IN `+appKinds+`
		ORDER BY r.created_at, r.id`, userID,
		func(rows *sql.Rows) error {
			var r ResearchRequest
			var created string
			var cid sql.NullInt64
			if err := rows.Scan(&r.ID, &r.Kind, &r.TargetID, &created, &r.Name, &r.JobURL, &cid, &r.Recipient); err != nil {
				return err
			}
			if cid.Valid {
				r.ContactID = &cid.Int64
			}
			r.CreatedAt, _ = time.Parse(time.RFC3339, created)
			out = append(out, r)
			return nil
		})
	return out, err
}

// researchRequestedAt reports when a pending request was made (nil when none).
func (s *Store) researchRequestedAt(ctx context.Context, userID, kind string, targetID int64) (*time.Time, error) {
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM research_requests WHERE user_id = ? AND kind = ? AND target_id = ? AND status = 'pending'`,
		userID, kind, targetID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, _ := time.Parse(time.RFC3339, created)
	return &at, nil
}

// maxFitsPerRefresh caps how many fit scores one refresh can queue.
const maxFitsPerRefresh = 20

// queueAfterRefresh queues an Opus fit score for each new open application without
// one. (The sync service then asks the quiz service for today's quiz.)
// prev is the previous refresh time; on the very first refresh no fit scores are
// queued, so a new mailbox doesn't flood the queue with its whole history.
func (s *Store) queueAfterRefresh(ctx context.Context, userID, prev string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if prev != "" {
		var ids []int64
		rows, err := tx.QueryContext(ctx, `SELECT a.id FROM applications a
			WHERE a.user_id = ? AND (SELECT MIN(`+arrived+`) FROM events e WHERE e.application_id = a.id) > ?
			  AND EXISTS (SELECT 1 FROM events e WHERE e.application_id = a.id AND e.kind = 'applied')
			  AND COALESCE(a.stage_override, a.stage) <> 'rejected'
			  AND NOT EXISTS (SELECT 1 FROM fit_scores f WHERE f.application_id = a.id)
			ORDER BY a.applied_at DESC LIMIT ?`, userID, prev, maxFitsPerRefresh)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := queue(ctx, tx, userID, "fit_score", id, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// queue adds a pending request inside a transaction (once per kind and target).
func queue(ctx context.Context, tx *sql.Tx, userID, kind string, targetID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO research_requests (user_id, kind, target_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, userID, kind, targetID, now.UTC().Format(time.RFC3339))
	return err
}

func markResearchDone(ctx context.Context, tx *sql.Tx, userID, kind string, targetID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE research_requests SET status = 'done', done_at = ? WHERE user_id = ? AND kind = ? AND target_id = ? AND status = 'pending'`,
		now.UTC().Format(time.RFC3339), userID, kind, targetID)
	return err
}

// RequestOutreach queues a cold email for Claude, for a contact on the application
// or a recipient the user typed ("Priya, HR"). Asking again while one is pending
// updates who it's for.
func (s *Store) RequestOutreach(ctx context.Context, userID string, appID int64, contactID *int64, recipient string, now time.Time) (time.Time, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	if err := ownsApp(ctx, tx, userID, appID); err != nil {
		return time.Time{}, err
	}
	var cid any
	if contactID != nil {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE id = ? AND user_id = ? AND application_id = ?`,
			*contactID, userID, appID).Scan(&n); err != nil {
			return time.Time{}, err
		}
		if n == 0 {
			return time.Time{}, ErrBadContact
		}
		cid = *contactID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO research_requests (user_id, kind, target_id, created_at, contact_id, recipient)
		VALUES (?, 'outreach', ?, ?, ?, ?)
		ON CONFLICT (user_id, kind, target_id) WHERE status = 'pending'
		DO UPDATE SET contact_id = excluded.contact_id, recipient = excluded.recipient`,
		userID, appID, now.UTC().Format(time.RFC3339), cid, truncate(strings.TrimSpace(recipient), 120)); err != nil {
		return time.Time{}, err
	}
	var created string
	if err := tx.QueryRowContext(ctx, `SELECT created_at FROM research_requests WHERE user_id = ? AND kind = 'outreach' AND target_id = ? AND status = 'pending'`,
		userID, appID).Scan(&created); err != nil {
		return time.Time{}, err
	}
	at, _ := time.Parse(time.RFC3339, created)
	return at, tx.Commit()
}

// MarkDraftsSeen clears an application's "new draft" flag (the user opened it).
func (s *Store) MarkDraftsSeen(ctx context.Context, userID string, appID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE outreach_drafts SET seen_at = ? WHERE user_id = ? AND application_id = ? AND seen_at IS NULL`,
		now.UTC().Format(time.RFC3339), userID, appID)
	return err
}

// draftStates says, per application, where its cold email stands: queued (a request
// is pending), ready (a draft not opened yet) or drafted (opened). Absent: none.
func (s *Store) draftStates(ctx context.Context, userID string) (map[int64]string, error) {
	out := map[int64]string{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT application_id, CASE WHEN SUM(seen_at IS NULL) > 0 THEN 'ready' ELSE 'drafted' END
		FROM outreach_drafts WHERE user_id = ? GROUP BY application_id
		UNION ALL
		SELECT target_id, 'queued' FROM research_requests WHERE user_id = ? AND kind = 'outreach' AND status = 'pending'`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var st string
		if err := rows.Scan(&id, &st); err != nil {
			return nil, err
		}
		// A pending request shows as queued; otherwise an unopened draft as ready.
		switch {
		case st == "queued":
			out[id] = st
		case out[id] == "queued":
		case st == "ready" || out[id] == "":
			out[id] = st
		}
	}
	return out, rows.Err()
}
