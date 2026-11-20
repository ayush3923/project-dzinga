package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Improvement drills: the specific things feedback asks the user to do next time
// ("one north-star metric, not four", "no 'I believe'"). Feedback on a later
// written or spoken answer checks each open drill of that kind: met, partly or
// missed. How well drills are followed feeds the readiness index.

const maxDrillsPerFeedback = 4

type Drill struct {
	ID         int64      `json:"id"`
	Day        string     `json:"day"`  // the quiz day it came from
	Kind       string     `json:"kind"` // subjective | voice | any
	Text       string     `json:"text"`
	Status     string     `json:"status"` // open | met | partly | missed
	CheckedDay string     `json:"checked_day,omitempty"`
	CheckNote  string     `json:"check_note,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	CheckedAt  *time.Time `json:"checked_at"`
}

// NewDrill is a drill set by feedback.
type NewDrill struct {
	Kind string `json:"kind"` // subjective | voice | any
	Text string `json:"text"`
}

// DrillCheck is feedback's verdict on an open drill, judged against this answer.
type DrillCheck struct {
	DrillID int64  `json:"drill_id"`
	Result  string `json:"result"` // met | partly | missed
	Note    string `json:"note"`
}

var ErrBadDrill = errors.New("invalid drill or check")

func validateDrills(drills []NewDrill, checks []DrillCheck) error {
	if len(drills) > maxDrillsPerFeedback {
		return fmt.Errorf("%w: at most %d drills per feedback", ErrBadDrill, maxDrillsPerFeedback)
	}
	for _, d := range drills {
		if d.Kind != "subjective" && d.Kind != "voice" && d.Kind != "any" {
			return fmt.Errorf("%w: kind must be subjective, voice or any", ErrBadDrill)
		}
		if n := len([]rune(strings.TrimSpace(d.Text))); n < 10 || n > 300 {
			return fmt.Errorf("%w: a drill is 10-300 characters", ErrBadDrill)
		}
	}
	for _, c := range checks {
		if c.Result != "met" && c.Result != "partly" && c.Result != "missed" {
			return fmt.Errorf("%w: result must be met, partly or missed", ErrBadDrill)
		}
		if len([]rune(c.Note)) > 300 {
			return fmt.Errorf("%w: a check note is at most 300 characters", ErrBadDrill)
		}
	}
	return nil
}

// SaveFeedback stores Claude's feedback on a written or spoken answer, the drills
// it sets (replacing any it set before) and its checks of earlier open drills.
// A check must be of an open drill from an earlier day whose kind matches this
// answer (or any).
func (s *Store) SaveFeedback(ctx context.Context, userID string, questionID int64, transcript, feedback string, score int,
	drills []NewDrill, checks []DrillCheck, now time.Time) error {
	if strings.TrimSpace(feedback) == "" || score < 0 || score > 10 {
		return errors.New("feedback is required and score must be 0-10")
	}
	if err := validateDrills(drills, checks); err != nil {
		return err
	}
	x, err := s.Question(ctx, userID, questionID, true)
	if err != nil {
		return err
	}
	if x.Kind == "mcq" || x.Response == nil {
		return ErrBadAnswer
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var day string
	if err := tx.QueryRowContext(ctx, `SELECT z.day FROM quiz_questions qq JOIN quizzes z ON z.id = qq.quiz_id WHERE qq.id = ?`, questionID).Scan(&day); err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `UPDATE quiz_answers SET transcript = ?, feedback = ?, score = ?, feedback_at = ? WHERE question_id = ?`,
		truncate(strings.TrimSpace(transcript), 8000), truncate(strings.TrimSpace(feedback), 2000), score, at, questionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drills WHERE user_id = ? AND from_question_id = ? AND status = 'open'`, userID, questionID); err != nil {
		return err
	}
	for i, d := range drills {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drills (user_id, from_question_id, day, kind, position, text, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, questionID, day, d.Kind, i, strings.TrimSpace(d.Text), at); err != nil {
			return err
		}
	}
	for _, c := range checks {
		res, err := tx.ExecContext(ctx, `UPDATE drills SET status = ?, check_note = ?, checked_question_id = ?, checked_day = ?, checked_at = ?
			WHERE id = ? AND user_id = ? AND status = 'open' AND day < ? AND kind IN (?, 'any')`,
			c.Result, strings.TrimSpace(c.Note), questionID, day, at, c.DrillID, userID, day, x.Kind)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: drill %d is not an open %s drill from an earlier day", ErrBadDrill, c.DrillID, x.Kind)
		}
	}
	return tx.Commit()
}

// OpenDrills lists open drills that apply to an answer of this kind on this day
// (set on an earlier day), oldest first. kind "" lists every open drill.
func (s *Store) OpenDrills(ctx context.Context, userID, kind, beforeDay string) ([]Drill, error) {
	q := `SELECT ` + drillColumns + ` FROM drills WHERE user_id = ? AND status = 'open'`
	args := []any{userID}
	if kind != "" {
		q += ` AND kind IN (?, 'any') AND day < ?`
		args = append(args, kind, beforeDay)
	}
	return s.drills(ctx, q+` ORDER BY day, position, id`, args)
}

// RecentChecks lists drills checked on or after a day, newest first.
func (s *Store) RecentChecks(ctx context.Context, userID, since string) ([]Drill, error) {
	return s.drills(ctx, `SELECT `+drillColumns+` FROM drills WHERE user_id = ? AND status <> 'open' AND checked_day >= ?
		ORDER BY checked_day DESC, id DESC`, []any{userID, since})
}

// FollowThrough is the share of drills checked since a day that were followed
// (met = 1, partly = ½), 0-100; nil when none were checked.
func (s *Store) FollowThrough(ctx context.Context, userID, since string) (*int, int, error) {
	var checked int
	var score sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*), SUM(CASE status WHEN 'met' THEN 1.0 WHEN 'partly' THEN 0.5 ELSE 0 END)
		FROM drills WHERE user_id = ? AND status <> 'open' AND checked_day >= ?`, userID, since).Scan(&checked, &score)
	if err != nil || checked == 0 {
		return nil, checked, err
	}
	v := int(score.Float64/float64(checked)*100 + 0.5)
	return &v, checked, nil
}

const drillColumns = `id, day, kind, text, status, COALESCE(checked_day, ''), check_note, created_at, checked_at`

func (s *Store) drills(ctx context.Context, q string, args []any) ([]Drill, error) {
	out := []Drill{}
	err := s.queryRows(ctx, q, args, func(r *sql.Rows) error {
		var d Drill
		var created string
		var checked sql.NullString
		err := r.Scan(&d.ID, &d.Day, &d.Kind, &d.Text, &d.Status, &d.CheckedDay, &d.CheckNote, &created, &checked)
		d.CreatedAt, _ = time.Parse(time.RFC3339, created)
		if checked.Valid {
			t, _ := time.Parse(time.RFC3339, checked.String)
			d.CheckedAt = &t
		}
		out = append(out, d)
		return err
	})
	return out, err
}

// QuestionDay is the quiz day a question belongs to.
func (s *Store) QuestionDay(ctx context.Context, userID string, questionID int64) (string, error) {
	var day string
	err := s.db.QueryRowContext(ctx, `SELECT z.day FROM quiz_questions qq JOIN quizzes z ON z.id = qq.quiz_id WHERE qq.id = ? AND z.user_id = ?`,
		questionID, userID).Scan(&day)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return day, err
}
