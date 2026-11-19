package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Interview notes and reviews. While an application is interviewing, the user
// writes how a round went (the questions, the feedback, how they felt). Saving a
// note queues an interview_review for Claude in the app (docs/runbooks/claude-queue.md),
// which reads every note with the posting, fit score and profile and comments on
// the chances of selection. Reviews are kept; the newest is shown.

var ErrNotInterviewing = errors.New("interview notes are for applications in the interview stage")

type InterviewNote struct {
	ID        int64     `json:"id"`
	Round     int       `json:"round"` // 0 when not given
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

func (n InterviewNote) Validate() error {
	b := strings.TrimSpace(n.Body)
	if len([]rune(b)) < 10 || len([]rune(b)) > 4000 {
		return errors.New("write 10-4000 characters about the interview")
	}
	if n.Round < 0 || n.Round > 20 {
		return errors.New("round must be between 0 and 20")
	}
	return nil
}

type InterviewReview struct {
	Chance    string    `json:"chance"` // low | medium | high
	Summary   string    `json:"summary"`
	Signals   []string  `json:"signals"`    // what points towards selection
	Concerns  []string  `json:"concerns"`   // what points against it
	NextSteps []string  `json:"next_steps"` // what to do before the next round
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

func (r InterviewReview) Validate() error {
	if r.Chance != "low" && r.Chance != "medium" && r.Chance != "high" {
		return errors.New("chance must be low, medium or high")
	}
	if s := len([]rune(strings.TrimSpace(r.Summary))); s < 20 || s > 1200 {
		return errors.New("summary must be 20-1200 characters")
	}
	if r.Model == "" {
		return errors.New("model is required")
	}
	for _, l := range [][]string{r.Signals, r.Concerns, r.NextSteps} {
		if len(l) > 6 {
			return errors.New("at most 6 signals, concerns and next steps each")
		}
		for _, x := range l {
			if strings.TrimSpace(x) == "" || len([]rune(x)) > 400 {
				return errors.New("each item must be 1-400 characters")
			}
		}
	}
	return nil
}

// AddInterviewNote saves a note and queues a review. Only an application in the
// interview stage (from its emails or set by hand) takes notes.
func (s *Store) AddInterviewNote(ctx context.Context, userID string, appID int64, n InterviewNote, now time.Time) (InterviewNote, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return n, err
	}
	defer tx.Rollback()
	var stage string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(stage_override, stage) FROM applications WHERE user_id = ? AND id = ?`, userID, appID).Scan(&stage)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, err
	}
	if stage != "interview" {
		return n, ErrNotInterviewing
	}
	n.Body = strings.TrimSpace(n.Body)
	n.CreatedAt = now.UTC().Truncate(time.Second)
	res, err := tx.ExecContext(ctx, `INSERT INTO interview_notes (user_id, application_id, round, body, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, appID, n.Round, n.Body, n.CreatedAt.Format(time.RFC3339))
	if err != nil {
		return n, err
	}
	n.ID, _ = res.LastInsertId()
	if err := queue(ctx, tx, userID, "interview_review", appID, now); err != nil {
		return n, err
	}
	return n, tx.Commit()
}

// SaveInterviewReview saves Claude's review and completes the queued request.
func (s *Store) SaveInterviewReview(ctx context.Context, userID string, appID int64, r InterviewReview, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownsApp(ctx, tx, userID, appID); err != nil {
		return err
	}
	enc := func(l []string) string {
		if l == nil {
			l = []string{}
		}
		b, _ := json.Marshal(l)
		return string(b)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO interview_reviews (user_id, application_id, chance, summary, signals, concerns, next_steps, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, userID, appID, r.Chance, strings.TrimSpace(r.Summary),
		enc(r.Signals), enc(r.Concerns), enc(r.NextSteps), truncate(r.Model, 100), now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if err := markResearchDone(ctx, tx, userID, "interview_review", appID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// interviewNotes lists an application's notes, oldest first.
func (s *Store) interviewNotes(ctx context.Context, appID int64) ([]InterviewNote, error) {
	out := []InterviewNote{}
	err := s.scanRows(ctx, `SELECT id, round, body, created_at FROM interview_notes WHERE application_id = ? ORDER BY created_at, id`, appID,
		func(r *sql.Rows) error {
			var n InterviewNote
			var created string
			err := r.Scan(&n.ID, &n.Round, &n.Body, &created)
			n.CreatedAt, _ = time.Parse(time.RFC3339, created)
			out = append(out, n)
			return err
		})
	return out, err
}

// latestInterviewReview returns the newest review (nil when there is none).
func (s *Store) latestInterviewReview(ctx context.Context, appID int64) (*InterviewReview, error) {
	var r InterviewReview
	var signals, concerns, steps, created string
	err := s.db.QueryRowContext(ctx, `SELECT chance, summary, signals, concerns, next_steps, model, created_at FROM interview_reviews
		WHERE application_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, appID).
		Scan(&r.Chance, &r.Summary, &signals, &concerns, &steps, &r.Model, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339, created)
	for _, x := range []struct {
		src string
		dst *[]string
	}{{signals, &r.Signals}, {concerns, &r.Concerns}, {steps, &r.NextSteps}} {
		*x.dst = []string{}
		if err := json.Unmarshal([]byte(x.src), x.dst); err != nil {
			return nil, err
		}
	}
	return &r, nil
}
