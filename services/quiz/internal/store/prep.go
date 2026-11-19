package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Prep questions: three bonus questions a day, shown on the Today page. Claude
// writes them with the quiz, aimed first at the companies the user is
// interviewing with (their postings and stage), each with why it is likely to
// come up and how to structure a strong answer. They are not graded.

const prepPerDay = 3

type PrepQuestion struct {
	Company  string `json:"company"` // "" for a general question
	Role     string `json:"role"`
	Question string `json:"question"`
	Why      string `json:"why"`
	Approach string `json:"approach"`
}

type Prep struct {
	Day   string         `json:"day"`
	Items []PrepQuestion `json:"items"`
}

func (p Prep) Validate() error {
	if !reDay.MatchString(p.Day) {
		return errors.New("day must be YYYY-MM-DD")
	}
	if len(p.Items) != prepPerDay {
		return fmt.Errorf("send exactly %d prep questions", prepPerDay)
	}
	for i, x := range p.Items {
		n := func(s string) int { return len([]rune(strings.TrimSpace(s))) }
		switch {
		case n(x.Company) > 120 || n(x.Role) > 200:
			return fmt.Errorf("item %d: company ≤ 120 characters, role ≤ 200", i+1)
		case n(x.Question) < 15 || n(x.Question) > 600:
			return fmt.Errorf("item %d: question must be 15-600 characters", i+1)
		case n(x.Why) < 10 || n(x.Why) > 400:
			return fmt.Errorf("item %d: why must be 10-400 characters", i+1)
		case n(x.Approach) < 20 || n(x.Approach) > 1200:
			return fmt.Errorf("item %d: approach must be 20-1200 characters", i+1)
		}
	}
	return nil
}

// SavePrep replaces a day's prep questions.
func (s *Store) SavePrep(ctx context.Context, userID string, p Prep) error {
	if err := p.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM prep_questions WHERE user_id = ? AND day = ?`, userID, p.Day); err != nil {
		return err
	}
	for i, x := range p.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO prep_questions (user_id, day, position, company, role, question, why, approach)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, userID, p.Day, i, strings.TrimSpace(x.Company), strings.TrimSpace(x.Role),
			strings.TrimSpace(x.Question), strings.TrimSpace(x.Why), strings.TrimSpace(x.Approach)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestPrep returns the newest day's prep questions on or before day (none when empty).
func (s *Store) LatestPrep(ctx context.Context, userID, day string) (Prep, error) {
	p := Prep{Items: []PrepQuestion{}}
	err := s.db.QueryRowContext(ctx, `SELECT day FROM prep_questions WHERE user_id = ? AND day <= ? ORDER BY day DESC LIMIT 1`,
		userID, day).Scan(&p.Day)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = s.queryRows(ctx, `SELECT company, role, question, why, approach FROM prep_questions
		WHERE user_id = ? AND day = ? ORDER BY position`, []any{userID, p.Day}, func(r *sql.Rows) error {
		var x PrepQuestion
		err := r.Scan(&x.Company, &x.Role, &x.Question, &x.Why, &x.Approach)
		p.Items = append(p.Items, x)
		return err
	})
	return p, err
}
