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

// Organization briefings: what Claude learns about a company while building the
// daily quiz (its org_news questions), saved on the organization so the user sees
// it in the organization drawer. The quiz service keeps the quiz; core-api keeps
// what belongs to the organization. Saving news or new facts flags the
// organization until the user opens it.

var reDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// maxBriefingNews caps how many news items an organization keeps (newest first).
const maxBriefingNews = 10

type BriefingNews struct {
	Day         string `json:"day"` // the quiz day it was found for
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	SourceTitle string `json:"source_title"`
	URL         string `json:"url"`
	PublishedOn string `json:"published_on"`
}

// Figure is a published number as written ("$177.6B"), with the period it covers.
type Figure struct {
	Value  string `json:"value"`
	Period string `json:"period"`
	URL    string `json:"url"`
}

type Briefing struct {
	CEO            string         `json:"ceo"`
	CPO            string         `json:"cpo"` // or the nearest product head, with title
	RevenueYear    Figure         `json:"revenue_year"`
	RevenueQuarter Figure         `json:"revenue_quarter"`
	LeadersURL     string         `json:"leaders_url"`
	News           []BriefingNews `json:"news"`
	Model          string         `json:"model"`
	UpdatedAt      time.Time      `json:"updated_at"`
	SeenAt         *time.Time     `json:"seen_at"`
}

func httpsOrEmpty(u string) bool {
	return u == "" || (strings.HasPrefix(u, "https://") && len(u) <= 2000)
}

func (b Briefing) Validate() error {
	if b.Model == "" {
		return errors.New("model is required")
	}
	if len([]rune(b.CEO)) > 120 || len([]rune(b.CPO)) > 160 {
		return errors.New("ceo is at most 120 characters, cpo at most 160")
	}
	for _, f := range []Figure{b.RevenueYear, b.RevenueQuarter} {
		if len([]rune(f.Value)) > 60 || len([]rune(f.Period)) > 80 || !httpsOrEmpty(f.URL) {
			return errors.New("revenue: value ≤ 60 characters, period ≤ 80, an https source")
		}
		if f.Value != "" && (f.Period == "" || f.URL == "") {
			return errors.New("revenue needs the period it covers and a source")
		}
	}
	if !httpsOrEmpty(b.LeadersURL) {
		return errors.New("leaders_url must be https")
	}
	if len(b.News) > 5 {
		return errors.New("at most 5 news items per save")
	}
	for i, n := range b.News {
		switch {
		case !reDay.MatchString(n.Day):
			return fmt.Errorf("news %d: day must be YYYY-MM-DD", i+1)
		case len([]rune(strings.TrimSpace(n.Title))) < 5 || len([]rune(n.Title)) > 200:
			return fmt.Errorf("news %d: title must be 5-200 characters", i+1)
		case len([]rune(n.Summary)) > 600:
			return fmt.Errorf("news %d: summary is at most 600 characters", i+1)
		case n.URL == "" || !httpsOrEmpty(n.URL):
			return fmt.Errorf("news %d: an https source link is required", i+1)
		case n.PublishedOn != "" && !reDay.MatchString(n.PublishedOn):
			return fmt.Errorf("news %d: published_on must be YYYY-MM-DD", i+1)
		}
	}
	return nil
}

// SaveBriefing updates an organization's facts and adds news (a link already saved
// is kept as it was). The organization is flagged until the user opens it.
func (s *Store) SaveBriefing(ctx context.Context, userID string, orgID int64, b Briefing, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownsOrg(ctx, tx, userID, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO org_briefings (organization_id, user_id, ceo, cpo,
			revenue_year, revenue_year_period, revenue_year_url, revenue_quarter, revenue_quarter_period, revenue_quarter_url,
			leaders_url, model, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (organization_id) DO UPDATE SET ceo = excluded.ceo, cpo = excluded.cpo,
			revenue_year = excluded.revenue_year, revenue_year_period = excluded.revenue_year_period, revenue_year_url = excluded.revenue_year_url,
			revenue_quarter = excluded.revenue_quarter, revenue_quarter_period = excluded.revenue_quarter_period,
			revenue_quarter_url = excluded.revenue_quarter_url, leaders_url = excluded.leaders_url,
			model = excluded.model, updated_at = excluded.updated_at`,
		orgID, userID, strings.TrimSpace(b.CEO), strings.TrimSpace(b.CPO),
		strings.TrimSpace(b.RevenueYear.Value), strings.TrimSpace(b.RevenueYear.Period), b.RevenueYear.URL,
		strings.TrimSpace(b.RevenueQuarter.Value), strings.TrimSpace(b.RevenueQuarter.Period), b.RevenueQuarter.URL,
		b.LeadersURL, truncate(b.Model, 100), now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	for _, n := range b.News {
		if _, err := tx.ExecContext(ctx, `INSERT INTO org_briefing_news (organization_id, day, title, summary, source_title, url, published_on)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (organization_id, url) DO NOTHING`,
			orgID, n.Day, strings.TrimSpace(n.Title), strings.TrimSpace(n.Summary), truncate(strings.TrimSpace(n.SourceTitle), 120),
			n.URL, n.PublishedOn); err != nil {
			return err
		}
	}
	// Keep the newest items only.
	if _, err := tx.ExecContext(ctx, `DELETE FROM org_briefing_news WHERE organization_id = ? AND id NOT IN (
		SELECT id FROM org_briefing_news WHERE organization_id = ?
		ORDER BY COALESCE(NULLIF(published_on, ''), day) DESC, id DESC LIMIT ?)`, orgID, orgID, maxBriefingNews); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkSeen clears the organization's flags: what's new from the daily quiz and a
// new diagnosis. Opening the organization counts as seeing both.
func (s *Store) MarkSeen(ctx context.Context, userID string, orgID int64, now time.Time) error {
	if err := ownsOrg(ctx, s.db, userID, orgID); err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(ctx, `UPDATE org_briefings SET seen_at = ? WHERE organization_id = ? AND user_id = ?`, at, orgID, userID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE org_diagnoses SET seen_at = ? WHERE organization_id = ? AND user_id = ? AND seen_at IS NULL`, at, orgID, userID)
	return err
}

// briefing reads an organization's briefing (nil when there is none).
func (s *Store) briefing(ctx context.Context, orgID int64) (*Briefing, error) {
	var b Briefing
	var updated string
	var seen sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT ceo, cpo, revenue_year, revenue_year_period, revenue_year_url,
		revenue_quarter, revenue_quarter_period, revenue_quarter_url, leaders_url, model, updated_at, seen_at
		FROM org_briefings WHERE organization_id = ?`, orgID).
		Scan(&b.CEO, &b.CPO, &b.RevenueYear.Value, &b.RevenueYear.Period, &b.RevenueYear.URL,
			&b.RevenueQuarter.Value, &b.RevenueQuarter.Period, &b.RevenueQuarter.URL, &b.LeadersURL, &b.Model, &updated, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	b.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if seen.Valid {
		t, _ := time.Parse(time.RFC3339, seen.String)
		b.SeenAt = &t
	}
	b.News = []BriefingNews{}
	err = s.scanRows(ctx, `SELECT day, title, summary, source_title, url, published_on FROM org_briefing_news
		WHERE organization_id = ? ORDER BY COALESCE(NULLIF(published_on, ''), day) DESC, id DESC`, orgID,
		func(r *sql.Rows) error {
			var n BriefingNews
			err := r.Scan(&n.Day, &n.Title, &n.Summary, &n.SourceTitle, &n.URL, &n.PublishedOn)
			b.News = append(b.News, n)
			return err
		})
	return &b, err
}
