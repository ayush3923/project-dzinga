package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Company research: the user asks for a company profile; Claude in the app does
// the web research and saves it (SaveCompanyProfile), which completes the request.

type ResearchRequest struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`      // always company_profile
	TargetID  int64     `json:"target_id"` // the organization
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// RequestResearch queues a profile (once: a pending request keeps its first time).
func (s *Store) RequestResearch(ctx context.Context, userID string, orgID int64, now time.Time) (time.Time, error) {
	if err := ownsOrg(ctx, s.db, userID, orgID); err != nil {
		return time.Time{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO research_requests (user_id, organization_id, created_at) VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING`, userID, orgID, now.UTC().Format(time.RFC3339)); err != nil {
		return time.Time{}, err
	}
	at, err := s.researchRequestedAt(ctx, userID, orgID)
	if err != nil || at == nil {
		return time.Time{}, err
	}
	return *at, nil
}

// PendingResearch lists the queue, oldest first.
func (s *Store) PendingResearch(ctx context.Context, userID string) ([]ResearchRequest, error) {
	out := []ResearchRequest{}
	err := s.queryRows(ctx, `SELECT r.id, r.organization_id, o.name, r.created_at FROM research_requests r
		JOIN organizations o ON o.id = r.organization_id
		WHERE r.user_id = ? AND r.status = 'pending' ORDER BY r.created_at, r.id`, []any{userID},
		func(rows *sql.Rows) error {
			r := ResearchRequest{Kind: "company_profile"}
			var created string
			err := rows.Scan(&r.ID, &r.TargetID, &r.Name, &created)
			r.CreatedAt, _ = time.Parse(time.RFC3339, created)
			out = append(out, r)
			return err
		})
	return out, err
}

func (s *Store) researchRequestedAt(ctx context.Context, userID string, orgID int64) (*time.Time, error) {
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM research_requests WHERE user_id = ? AND organization_id = ? AND status = 'pending'`,
		userID, orgID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, _ := time.Parse(time.RFC3339, created)
	return &at, nil
}

func markResearchDone(ctx context.Context, tx *sql.Tx, userID string, orgID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE research_requests SET status = 'done', done_at = ? WHERE user_id = ? AND organization_id = ? AND status = 'pending'`,
		now.UTC().Format(time.RFC3339), userID, orgID)
	return err
}
