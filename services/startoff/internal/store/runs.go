package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Daily runs: what startoff did each day and how each step went. A run is started
// by the 07:00 timer, the Refresh button or Claude; only one runs at a time.

var ErrRunning = errors.New("a daily run is already in progress")

const (
	TriggerTimer  = "timer"
	TriggerManual = "manual"
	TriggerClaude = "claude"
)

type Step struct {
	Name       string    `json:"name"`
	Status     string    `json:"status"` // done | skipped | failed
	Detail     string    `json:"detail"`
	FinishedAt time.Time `json:"finished_at"`
}

type Run struct {
	ID         int64      `json:"id"`
	Day        string     `json:"day"`
	Trigger    string     `json:"trigger"` // timer | manual | claude
	Status     string     `json:"status"`  // running | done | partial
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Steps      []Step     `json:"steps"`
}

func validTrigger(t string) bool {
	return t == TriggerTimer || t == TriggerManual || t == TriggerClaude
}

// StartRun records a new run, unless one is already running (ErrRunning, with that run).
func (s *Store) StartRun(ctx context.Context, userID, trigger, day string, now time.Time) (Run, error) {
	if !validTrigger(trigger) {
		return Run{}, errors.New("trigger must be timer, manual or claude")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Run{}, err
	}
	defer tx.Rollback()
	var running int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM runs WHERE user_id = ? AND status = 'running' ORDER BY id DESC LIMIT 1`, userID).Scan(&running)
	if err == nil {
		tx.Rollback()
		r, err := s.run(ctx, userID, running)
		if err != nil {
			return Run{}, err
		}
		return r, ErrRunning
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Run{}, err
	}
	at := now.UTC().Truncate(time.Second)
	res, err := tx.ExecContext(ctx, `INSERT INTO runs (user_id, day, trigger, status, started_at) VALUES (?, ?, ?, 'running', ?)`,
		userID, day, trigger, at.Format(time.RFC3339))
	if err != nil {
		return Run{}, err
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return Run{}, err
	}
	return Run{ID: id, Day: day, Trigger: trigger, Status: "running", StartedAt: at, Steps: []Step{}}, nil
}

// AddStep records how a step of a run went.
func (s *Store) AddStep(ctx context.Context, runID int64, st Step) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_steps (run_id, position, name, status, detail, finished_at)
		VALUES (?, (SELECT COUNT(*) FROM run_steps WHERE run_id = ?), ?, ?, ?, ?)`,
		runID, runID, st.Name, st.Status, truncate(st.Detail, 1000), st.FinishedAt.UTC().Format(time.RFC3339))
	return err
}

// FinishRun closes a run: done when every step was, partial otherwise.
func (s *Store) FinishRun(ctx context.Context, runID int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET finished_at = ?,
		status = CASE WHEN EXISTS (SELECT 1 FROM run_steps WHERE run_id = ? AND status = 'failed') THEN 'partial' ELSE 'done' END
		WHERE id = ?`, now.UTC().Format(time.RFC3339), runID, runID)
	return err
}

// CloseInterrupted closes runs left running when the service stopped.
func (s *Store) CloseInterrupted(ctx context.Context, now time.Time) error {
	at := now.UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_steps (run_id, position, name, status, detail, finished_at)
		SELECT id, (SELECT COUNT(*) FROM run_steps st WHERE st.run_id = runs.id), 'interrupted', 'failed', 'the service stopped during this run', ?
		FROM runs WHERE status = 'running'`, at)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE runs SET status = 'partial', finished_at = ? WHERE status = 'running'`, at)
	return err
}

// RanSince reports whether any run started at or after t.
func (s *Store) RanSince(ctx context.Context, userID string, t time.Time) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE user_id = ? AND started_at >= ?`, userID, t.UTC().Format(time.RFC3339)).Scan(&n)
	return n > 0, err
}

// Runs lists recent runs, newest first, with their steps.
func (s *Store) Runs(ctx context.Context, userID string, limit int) ([]Run, error) {
	var ids []int64
	err := s.queryRows(ctx, `SELECT id FROM runs WHERE user_id = ? ORDER BY id DESC LIMIT ?`, []any{userID, limit},
		func(r *sql.Rows) error {
			var id int64
			err := r.Scan(&id)
			ids = append(ids, id)
			return err
		})
	if err != nil {
		return nil, err
	}
	out := make([]Run, 0, len(ids))
	for _, id := range ids {
		r, err := s.run(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) run(ctx context.Context, userID string, id int64) (Run, error) {
	r := Run{Steps: []Step{}}
	var started string
	var finished sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, day, trigger, status, started_at, finished_at FROM runs WHERE user_id = ? AND id = ?`, userID, id).
		Scan(&r.ID, &r.Day, &r.Trigger, &r.Status, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	r.StartedAt, _ = time.Parse(time.RFC3339, started)
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339, finished.String)
		r.FinishedAt = &t
	}
	err = s.queryRows(ctx, `SELECT name, status, detail, finished_at FROM run_steps WHERE run_id = ? ORDER BY position`, []any{id},
		func(rows *sql.Rows) error {
			var st Step
			var at string
			err := rows.Scan(&st.Name, &st.Status, &st.Detail, &at)
			st.FinishedAt, _ = time.Parse(time.RFC3339, at)
			r.Steps = append(r.Steps, st)
			return err
		})
	return r, err
}
