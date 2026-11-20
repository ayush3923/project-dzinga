// Package jobs runs startoff's daily jobs (ADR 0011): refresh every mailbox,
// request the day's quiz, and top up the organization diagnoses Claude works
// through. The run starts at 07:00 local time each day (or when the Refresh button
// or Claude asks), and each step's outcome is recorded.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/ayush3923/project-dzinga/services/startoff/internal/peers"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/store"
)

type Orgs interface {
	Candidates(ctx context.Context) ([]store.Candidate, error)
}

type Ingest interface {
	Start(ctx context.Context) error
	State(ctx context.Context) (peers.SyncState, error)
}

type Quiz interface {
	RequestToday(ctx context.Context) error
}

type Runner struct {
	Store  *store.Store
	Orgs   Orgs
	Ingest Ingest
	Quiz   Quiz
	Log    *slog.Logger
	UserID string
	Now    func() time.Time
	Loc    *time.Location // the user's time zone: the day and the 07:00 start
	At     time.Duration  // daily start, after local midnight
	Poll   time.Duration  // how often to check the refresh
	// SyncTimeout bounds the wait for the refresh; the other steps still run.
	SyncTimeout time.Duration
	Rand        *rand.Rand // picks organizations to diagnose; nil = seeded per run
}

// Day is the local day for t.
func (r *Runner) Day(t time.Time) string { return t.In(r.Loc).Format("2006-01-02") }

// Begin records a new run (or returns the running one with store.ErrRunning).
// Execute does the work; the API runs it in the background.
func (r *Runner) Begin(ctx context.Context, trigger string) (store.Run, error) {
	now := r.Now()
	return r.Store.StartRun(ctx, r.UserID, trigger, r.Day(now), now)
}

// Execute runs every step of a run. A failed step doesn't stop the others; the run
// ends "partial".
func (r *Runner) Execute(ctx context.Context, run store.Run) {
	log := r.Log.With("run_id", run.ID, "trigger", run.Trigger)
	log.Info("run_started")
	step := func(name string, fn func() (string, error)) {
		detail, err := fn()
		st := store.Step{Name: name, Status: "done", Detail: detail, FinishedAt: r.Now()}
		if err != nil {
			st.Status, st.Detail = "failed", strings.TrimSpace(detail+" "+err.Error())
			log.Warn("run_step_failed", "step", name, "err", err)
		}
		if err := r.Store.AddStep(ctx, run.ID, st); err != nil {
			log.Error("run_step_not_saved", "step", name, "err", err)
		}
	}
	step("refresh", func() (string, error) { return r.refresh(ctx) })
	step("quiz", func() (string, error) {
		if err := r.Quiz.RequestToday(ctx); err != nil {
			return "", err
		}
		return "requested the quiz for " + run.Day, nil
	})
	step("diagnoses", func() (string, error) { return r.diagnoses(ctx, run.ID) })
	if err := r.Store.FinishRun(ctx, run.ID, r.Now()); err != nil {
		log.Error("run_not_closed", "err", err)
	}
	log.Info("run_finished")
}

// refresh starts a mail refresh and waits for it, then sums up each mailbox.
func (r *Runner) refresh(ctx context.Context) (string, error) {
	if err := r.Ingest.Start(ctx); err != nil {
		return "", err
	}
	deadline := r.Now().Add(r.SyncTimeout)
	var st peers.SyncState
	for {
		var err error
		if st, err = r.Ingest.State(ctx); err != nil {
			return "", err
		}
		if !st.Running {
			break
		}
		if r.Now().After(deadline) {
			return "", fmt.Errorf("the refresh was still running after %s", r.SyncTimeout)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(r.Poll):
		}
	}
	added, ok := 0, 0
	var failed []string
	for _, m := range st.Mailboxes {
		added += m.Added
		if m.Status == "error" {
			failed = append(failed, m.Address+": "+m.Error)
		} else {
			ok++
		}
	}
	detail := fmt.Sprintf("%d of %d mailboxes refreshed, %d new emails", ok, len(st.Mailboxes), added)
	switch {
	case st.Error != "":
		return detail, errors.New(st.Error)
	case len(failed) > 0:
		return detail, fmt.Errorf("failed: %s", strings.Join(failed, "; "))
	}
	return detail, nil
}

func (r *Runner) diagnoses(ctx context.Context, runID int64) (string, error) {
	cands, err := r.Orgs.Candidates(ctx)
	if err != nil {
		return "", err
	}
	queued, err := r.Store.QueueDiagnoses(ctx, r.UserID, cands, store.DiagnosesPerRun, runID, r.Now(), r.Rand)
	if err != nil {
		return "", err
	}
	if len(queued) == 0 {
		return "the queue already holds 5 diagnoses, or nothing is due", nil
	}
	names := make([]string, len(queued))
	for i, q := range queued {
		names[i] = q.Name
	}
	return fmt.Sprintf("queued %d: %s", len(queued), strings.Join(names, ", ")), nil
}

// Due reports whether today's scheduled run should start now: it's past the start
// time and no run (of any kind) has started since.
func (r *Runner) Due(ctx context.Context) (bool, error) {
	now := r.Now().In(r.Loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, r.Loc)
	start := midnight.Add(r.At)
	if now.Before(start) {
		return false, nil
	}
	ran, err := r.Store.RanSince(ctx, r.UserID, start)
	return !ran, err
}

// Schedule checks every minute (and at once) whether the daily run is due, so a run
// missed while the Mac slept or the service was down starts when it's back.
func (r *Runner) Schedule(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		due, err := r.Due(ctx)
		if err != nil {
			r.Log.Error("schedule_check_failed", "err", err)
		} else if due {
			if run, err := r.Begin(ctx, store.TriggerTimer); err == nil {
				r.Execute(ctx, run)
			} else if !errors.Is(err, store.ErrRunning) {
				r.Log.Error("scheduled_run_not_started", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
