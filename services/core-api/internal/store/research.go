package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Research requests: web research (finding a job posting, building a company
// profile) is done by Claude in the Claude app, not through the API. The web app
// queues a request; the user asks Claude in the app to work through the queue,
// and saving the result marks the request done.

var ErrBadKind = errors.New("kind must be company_profile or job_posting")

type ResearchRequest struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	TargetID  int64     `json:"target_id"`
	Name      string    `json:"name"`    // organization name, or "Company — Role"
	JobURL    string    `json:"job_url"` // job_posting: the link already known, if any
	CreatedAt time.Time `json:"created_at"`
}

// RequestResearch queues a request (once: a pending one is returned as is).
func (s *Store) RequestResearch(ctx context.Context, userID, kind string, targetID int64, now time.Time) (time.Time, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	switch kind {
	case "company_profile":
		err = ownsOrg(ctx, tx, userID, targetID)
	case "job_posting":
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
			COALESCE(o.name, a.company || CASE WHEN a.role <> '' THEN ' — ' || a.role ELSE '' END, ''), COALESCE(a.job_url, '')
		FROM research_requests r
		LEFT JOIN organizations o ON r.kind = 'company_profile' AND o.id = r.target_id
		LEFT JOIN applications a ON r.kind = 'job_posting' AND a.id = r.target_id
		WHERE r.user_id = ? AND r.status = 'pending' ORDER BY r.created_at, r.id`, userID,
		func(rows *sql.Rows) error {
			var r ResearchRequest
			var created string
			if err := rows.Scan(&r.ID, &r.Kind, &r.TargetID, &created, &r.Name, &r.JobURL); err != nil {
				return err
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

func markResearchDone(ctx context.Context, tx *sql.Tx, userID, kind string, targetID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE research_requests SET status = 'done', done_at = ? WHERE user_id = ? AND kind = ? AND target_id = ? AND status = 'pending'`,
		now.UTC().Format(time.RFC3339), userID, kind, targetID)
	return err
}
