package store

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"
)

// Claude's queue in this service. Two kinds of request:
//   - company_profile: the user asks for a profile; Claude in the app does the web
//     research and saves it (SaveCompanyProfile), which completes the request.
//   - diagnosis: why a company is or isn't moving. Each refresh queues a few
//     organizations at random (QueueDiagnoses) and the user can ask for one; Claude
//     reads the history and saves a diagnosis (SaveDiagnosis), which completes it.

const (
	KindProfile   = "company_profile"
	KindDiagnosis = "diagnosis"
)

var ErrBadKind = errors.New("kind must be company_profile or diagnosis")

type ResearchRequest struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`      // company_profile | diagnosis
	TargetID  int64     `json:"target_id"` // the organization
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// RequestResearch queues a request (once: a pending request keeps its first time).
func (s *Store) RequestResearch(ctx context.Context, userID, kind string, orgID int64, now time.Time) (time.Time, error) {
	if kind != KindProfile && kind != KindDiagnosis {
		return time.Time{}, ErrBadKind
	}
	if err := ownsOrg(ctx, s.db, userID, orgID); err != nil {
		return time.Time{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO research_requests (user_id, organization_id, kind, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, userID, orgID, kind, now.UTC().Format(time.RFC3339)); err != nil {
		return time.Time{}, err
	}
	at, err := s.researchRequestedAt(ctx, userID, kind, orgID)
	if err != nil || at == nil {
		return time.Time{}, err
	}
	return *at, nil
}

// DiagnosesPerRefresh is how many organizations each refresh queues for a diagnosis.
const DiagnosesPerRefresh = 5

// rediagnoseAfter is how long a diagnosis stays current when nothing new has happened.
const rediagnoseAfter = 14 * 24 * time.Hour

// QueueDiagnoses tops the queue up to n pending diagnoses, picking organizations at
// random (so a retried or repeated refresh doesn't pile up more). Candidates have
// applications, no diagnosis already queued, and either no diagnosis yet or one that
// is out of date (activity since it was written, or older than two weeks). Returns
// what was queued.
func (s *Store) QueueDiagnoses(ctx context.Context, userID string, apps []App, n int, now time.Time, rng *rand.Rand) ([]ResearchRequest, error) {
	out := []ResearchRequest{}
	rep, err := s.Organizations(ctx, userID, apps, now)
	if err != nil {
		return out, err
	}
	last := map[int64]time.Time{}
	err = s.queryRows(ctx, `SELECT organization_id, MAX(created_at) FROM org_diagnoses WHERE user_id = ? GROUP BY organization_id`, []any{userID},
		func(rows *sql.Rows) error {
			var id int64
			var at string
			err := rows.Scan(&id, &at)
			last[id], _ = time.Parse(time.RFC3339, at)
			return err
		})
	if err != nil {
		return out, err
	}
	pending := map[int64]bool{}
	err = s.queryRows(ctx, `SELECT organization_id FROM research_requests WHERE user_id = ? AND kind = ? AND status = 'pending'`,
		[]any{userID, KindDiagnosis}, func(rows *sql.Rows) error {
			var id int64
			err := rows.Scan(&id)
			pending[id] = true
			return err
		})
	if err != nil {
		return out, err
	}
	if n -= len(pending); n <= 0 {
		return out, nil
	}
	var cands []OrgSummary
	for _, o := range rep.Organizations {
		if o.Applications == 0 || pending[o.ID] {
			continue
		}
		if at, ok := last[o.ID]; ok {
			stale := now.Sub(at) >= rediagnoseAfter || (o.LastActivityAt != nil && o.LastActivityAt.After(at))
			if !stale {
				continue
			}
		}
		cands = append(cands, o)
	}
	if rng == nil {
		rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}
	rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
	if len(cands) > n {
		cands = cands[:n]
	}
	for _, o := range cands {
		at, err := s.RequestResearch(ctx, userID, KindDiagnosis, o.ID, now)
		if err != nil {
			return out, err
		}
		out = append(out, ResearchRequest{Kind: KindDiagnosis, TargetID: o.ID, Name: o.Name, CreatedAt: at})
	}
	return out, nil
}

// PendingResearch lists the queue, oldest first.
func (s *Store) PendingResearch(ctx context.Context, userID string) ([]ResearchRequest, error) {
	out := []ResearchRequest{}
	err := s.queryRows(ctx, `SELECT r.id, r.kind, r.organization_id, o.name, r.created_at FROM research_requests r
		JOIN organizations o ON o.id = r.organization_id
		WHERE r.user_id = ? AND r.status = 'pending' ORDER BY r.created_at, r.id`, []any{userID},
		func(rows *sql.Rows) error {
			var r ResearchRequest
			var created string
			err := rows.Scan(&r.ID, &r.Kind, &r.TargetID, &r.Name, &created)
			r.CreatedAt, _ = time.Parse(time.RFC3339, created)
			out = append(out, r)
			return err
		})
	return out, err
}

func (s *Store) researchRequestedAt(ctx context.Context, userID, kind string, orgID int64) (*time.Time, error) {
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM research_requests WHERE user_id = ? AND kind = ? AND organization_id = ? AND status = 'pending'`,
		userID, kind, orgID).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	at, _ := time.Parse(time.RFC3339, created)
	return &at, nil
}

func markResearchDone(ctx context.Context, tx *sql.Tx, userID, kind string, orgID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE research_requests SET status = 'done', done_at = ? WHERE user_id = ? AND kind = ? AND organization_id = ? AND status = 'pending'`,
		now.UTC().Format(time.RFC3339), userID, kind, orgID)
	return err
}
