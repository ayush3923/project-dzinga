package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Experience struct {
	Title      string   `json:"title"`
	Company    string   `json:"company"`
	StartMonth string   `json:"start_month"` // YYYY-MM
	EndMonth   string   `json:"end_month"`   // YYYY-MM, "" = present
	Location   string   `json:"location"`
	Highlights []string `json:"highlights"`
}

type Profile struct {
	FullName       string       `json:"full_name"`
	Headline       string       `json:"headline"`
	CurrentTitle   string       `json:"current_title"`
	CurrentCompany string       `json:"current_company"`
	TotalYears     float64      `json:"total_years"`
	Location       string       `json:"location"`
	Summary        string       `json:"summary"`
	Skills         []string     `json:"skills"`
	Experiences    []Experience `json:"experiences"`
	UpdatedAt      *time.Time   `json:"updated_at"`
	// When the photo was last changed (nil = no photo). Read-only: set by the photo endpoints.
	PhotoUpdatedAt *time.Time `json:"photo_updated_at"`
}

var month = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

func (p Profile) Validate() error {
	if p.TotalYears < 0 || p.TotalYears > 60 {
		return errors.New("total_years out of range")
	}
	if len(p.Skills) > 60 || len(p.Experiences) > 30 {
		return errors.New("too many skills or experiences")
	}
	if len(p.Summary) > 3000 || len(p.Headline) > 200 || len(p.FullName) > 120 {
		return errors.New("field too long")
	}
	for i, e := range p.Experiences {
		if strings.TrimSpace(e.Title) == "" || strings.TrimSpace(e.Company) == "" {
			return fmt.Errorf("experience %d needs a title and company", i)
		}
		if (e.StartMonth != "" && !month.MatchString(e.StartMonth)) || (e.EndMonth != "" && !month.MatchString(e.EndMonth)) {
			return fmt.Errorf("experience %d: months must be YYYY-MM", i)
		}
		if e.StartMonth != "" && e.EndMonth != "" && e.EndMonth < e.StartMonth {
			return fmt.Errorf("experience %d ends before it starts", i)
		}
		if len(e.Highlights) > 12 {
			return fmt.Errorf("experience %d has too many highlights", i)
		}
	}
	return nil
}

func (s *Store) GetProfile(ctx context.Context, userID string) (Profile, error) {
	p := Profile{Skills: []string{}, Experiences: []Experience{}}
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT full_name, headline, current_title, current_company, total_years, location, summary, updated_at
		FROM profiles WHERE user_id = ?`, userID).
		Scan(&p.FullName, &p.Headline, &p.CurrentTitle, &p.CurrentCompany, &p.TotalYears, &p.Location, &p.Summary, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	} else if err != nil {
		return p, err
	}
	t, _ := time.Parse(time.RFC3339, at)
	p.UpdatedAt = &t
	if p.Skills, err = s.texts(ctx, `SELECT skill FROM profile_skills WHERE user_id = ? ORDER BY skill`, userID); err != nil {
		return p, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, company, start_month, end_month, location FROM profile_experiences
		WHERE user_id = ? ORDER BY position`, userID)
	if err != nil {
		return p, err
	}
	var ids []int64
	for rows.Next() {
		var e Experience
		var id int64
		if err := rows.Scan(&id, &e.Title, &e.Company, &e.StartMonth, &e.EndMonth, &e.Location); err != nil {
			rows.Close()
			return p, err
		}
		ids = append(ids, id)
		p.Experiences = append(p.Experiences, e)
	}
	rows.Close()
	for i, id := range ids {
		if p.Experiences[i].Highlights, err = s.texts(ctx, `SELECT text FROM profile_highlights WHERE experience_id = ? ORDER BY position`, id); err != nil {
			return p, err
		}
	}
	return p, nil
}

// PutProfile replaces the whole profile.
func (s *Store) PutProfile(ctx context.Context, userID string, p Profile, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles (user_id, full_name, headline, current_title, current_company, total_years, location, summary, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET full_name = excluded.full_name, headline = excluded.headline,
			current_title = excluded.current_title, current_company = excluded.current_company, total_years = excluded.total_years,
			location = excluded.location, summary = excluded.summary, updated_at = excluded.updated_at`,
		userID, p.FullName, p.Headline, p.CurrentTitle, p.CurrentCompany, p.TotalYears, p.Location, p.Summary,
		now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM profile_highlights WHERE experience_id IN (SELECT id FROM profile_experiences WHERE user_id = ?)`,
		`DELETE FROM profile_experiences WHERE user_id = ?`,
		`DELETE FROM profile_skills WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	for _, sk := range p.Skills {
		if sk = strings.TrimSpace(sk); sk != "" {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO profile_skills (user_id, skill) VALUES (?, ?)`, userID, truncate(sk, 80)); err != nil {
				return err
			}
		}
	}
	for i, e := range p.Experiences {
		res, err := tx.ExecContext(ctx, `INSERT INTO profile_experiences (user_id, position, title, company, start_month, end_month, location)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, userID, i, truncate(e.Title, 150), truncate(e.Company, 150), e.StartMonth, e.EndMonth, truncate(e.Location, 100))
		if err != nil {
			return err
		}
		eid, _ := res.LastInsertId()
		for j, h := range e.Highlights {
			if _, err := tx.ExecContext(ctx, `INSERT INTO profile_highlights (experience_id, position, text) VALUES (?, ?, ?)`,
				eid, j, truncate(h, 600)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
