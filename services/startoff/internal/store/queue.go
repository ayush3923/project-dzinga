package store

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"
)

// Claude's queue. Two kinds of request:
//   - company_profile: the user asks for a profile; Claude in the app does the web
//     research and saves it (SaveProfile), which completes the request.
//   - diagnosis: why a company is or isn't moving. Each daily run tops the queue up
//     with organizations picked at random (QueueDiagnoses) and the user can ask for
//     one; Claude saves it (SaveDiagnosis), which completes the request.

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
// runID is the daily run that queued it, or 0.
func (s *Store) RequestResearch(ctx context.Context, userID, kind string, org Org, runID int64, now time.Time) (time.Time, error) {
	if kind != KindProfile && kind != KindDiagnosis {
		return time.Time{}, ErrBadKind
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer tx.Rollback()
	if err := ensureCompany(ctx, tx, userID, org); err != nil {
		return time.Time{}, err
	}
	var run any
	if runID > 0 {
		run = runID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO research_requests (user_id, org_id, kind, run_id, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING`, userID, org.ID, kind, run, now.UTC().Format(time.RFC3339)); err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	at, err := s.requestedAt(ctx, userID, kind, org.ID)
	if err != nil || at == nil {
		return time.Time{}, err
	}
	return *at, nil
}

// DiagnosesPerRun is how many diagnoses each daily run keeps queued.
const DiagnosesPerRun = 5

// rediagnoseAfter is how long a diagnosis stays current when nothing new has happened.
const rediagnoseAfter = 14 * 24 * time.Hour

// Candidate is an organization that could be diagnosed (from the organizations service).
type Candidate struct {
	Org
	Applications   int
	LastActivityAt *time.Time
}

// QueueDiagnoses tops the queue up to n pending diagnoses, picking organizations at
// random (so a repeated run doesn't pile up more). Candidates have applications, no
// diagnosis already queued, and either no diagnosis yet or one that is out of date
// (activity since it was written, or older than two weeks). Returns what was queued.
func (s *Store) QueueDiagnoses(ctx context.Context, userID string, orgs []Candidate, n int, runID int64, now time.Time, rng *rand.Rand) ([]ResearchRequest, error) {
	out := []ResearchRequest{}
	last := map[int64]time.Time{}
	err := s.queryRows(ctx, `SELECT org_id, MAX(created_at) FROM diagnoses WHERE user_id = ? GROUP BY org_id`, []any{userID},
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
	err = s.queryRows(ctx, `SELECT org_id FROM research_requests WHERE user_id = ? AND kind = ? AND status = 'pending'`,
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
	var cands []Candidate
	for _, o := range orgs {
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
		at, err := s.RequestResearch(ctx, userID, KindDiagnosis, o.Org, runID, now)
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
	err := s.queryRows(ctx, `SELECT r.id, r.kind, r.org_id, c.name, r.created_at FROM research_requests r
		JOIN companies c ON c.user_id = r.user_id AND c.org_id = r.org_id
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

func (s *Store) requestedAt(ctx context.Context, userID, kind string, orgID int64) (*time.Time, error) {
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM research_requests WHERE user_id = ? AND kind = ? AND org_id = ? AND status = 'pending'`,
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

func markDone(ctx context.Context, tx *sql.Tx, userID, kind string, orgID int64, now time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE research_requests SET status = 'done', done_at = ? WHERE user_id = ? AND kind = ? AND org_id = ? AND status = 'pending'`,
		now.UTC().Format(time.RFC3339), userID, kind, orgID)
	return err
}
