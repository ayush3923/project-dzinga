package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
)

// ImportFromOrgs copies the research the organizations service held (profiles,
// quiz briefings and news, diagnoses, the research queue) before its migration v3
// dropped it, keeping organization ids. Each profile and briefing becomes the
// first version of its history. It runs once, into an empty database, and reads
// the source read-only (a backup file works). The source must be at v2.
func (s *Store) ImportFromOrgs(ctx context.Context, orgsPath string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM companies`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, errors.New("the startoff database is not empty; import only runs once")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	src := (&url.URL{Scheme: "file", Path: orgsPath, RawQuery: "mode=ro"}).String()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS orgs`, src); err != nil {
		return 0, err
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE orgs`)
	var version, tables int
	if err := conn.QueryRowContext(ctx, `PRAGMA orgs.user_version`).Scan(&version); err != nil {
		return 0, err
	}
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM orgs.sqlite_master WHERE type = 'table' AND name = 'org_diagnoses'`).Scan(&tables); err != nil {
		return 0, err
	}
	if version != 2 || tables == 0 {
		return 0, fmt.Errorf("%s is at v%d; import needs the organizations database at v2 (before its v3 dropped research; use a backup)", orgsPath, version)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	steps := []struct{ name, q string }{
		{"companies", `INSERT INTO main.companies (user_id, org_id, name, briefing_seen_at, diagnosis_seen_at)
			SELECT o.user_id, o.id, o.name,
				(SELECT b.seen_at FROM orgs.org_briefings b WHERE b.organization_id = o.id),
				(SELECT d.seen_at FROM orgs.org_diagnoses d WHERE d.organization_id = o.id ORDER BY d.created_at DESC, d.id DESC LIMIT 1)
			FROM orgs.organizations o WHERE o.id IN (
				SELECT organization_id FROM orgs.company_profiles UNION SELECT organization_id FROM orgs.org_briefings
				UNION SELECT organization_id FROM orgs.org_diagnoses UNION SELECT organization_id FROM orgs.research_requests)`},
		// One profile per organization there: its id becomes the profile id.
		{"profiles", `INSERT INTO main.profiles (id, user_id, org_id, overview, interview_angle, model, created_at)
			SELECT organization_id, user_id, organization_id, overview, interview_angle, model, created_at FROM orgs.company_profiles`},
		{"profile news", `INSERT INTO main.news (user_id, org_id, origin, profile_id, position, title, published_on, summary, url, created_at)
			SELECT p.user_id, n.organization_id, 'profile', n.organization_id, n.position, n.title, n.published_on, n.summary, n.url, p.created_at
			FROM orgs.company_news n JOIN orgs.company_profiles p ON p.organization_id = n.organization_id`},
		{"products", `INSERT INTO main.profile_products (profile_id, position, name, description)
			SELECT organization_id, position, name, description FROM orgs.company_products
			WHERE organization_id IN (SELECT organization_id FROM orgs.company_profiles)`},
		{"leaders", `INSERT INTO main.profile_leaders (profile_id, position, name, title)
			SELECT organization_id, position, name, title FROM orgs.company_leaders
			WHERE organization_id IN (SELECT organization_id FROM orgs.company_profiles)`},
		{"ratings", `INSERT INTO main.profile_ratings (profile_id, source, score, scale, review_count, url, as_of)
			SELECT organization_id, source, score, scale, review_count, url, as_of FROM orgs.company_ratings
			WHERE organization_id IN (SELECT organization_id FROM orgs.company_profiles)`},
		{"facts", `INSERT INTO main.facts (user_id, org_id, ceo, cpo, revenue_year, revenue_year_period, revenue_year_url,
				revenue_quarter, revenue_quarter_period, revenue_quarter_url, leaders_url, model, created_at)
			SELECT user_id, organization_id, ceo, cpo, revenue_year, revenue_year_period, revenue_year_url,
				revenue_quarter, revenue_quarter_period, revenue_quarter_url, leaders_url, model, updated_at FROM orgs.org_briefings`},
		{"quiz news", `INSERT INTO main.news (user_id, org_id, origin, day, title, summary, source_title, url, published_on, created_at)
			SELECT b.user_id, n.organization_id, 'quiz', n.day, n.title, n.summary, n.source_title, n.url, n.published_on, b.updated_at
			FROM orgs.org_briefing_news n JOIN orgs.org_briefings b ON b.organization_id = n.organization_id`},
		{"diagnoses", `INSERT INTO main.diagnoses (id, user_id, org_id, summary, model, created_at)
			SELECT id, user_id, organization_id, summary, model, created_at FROM orgs.org_diagnoses`},
		{"diagnosis items", `INSERT INTO main.diagnosis_items (diagnosis_id, kind, position, text, confidence)
			SELECT diagnosis_id, kind, position, text, confidence FROM orgs.org_diagnosis_items`},
		{"research requests", `INSERT INTO main.research_requests (user_id, org_id, kind, status, created_at, done_at)
			SELECT user_id, organization_id, kind, status, created_at, done_at FROM orgs.research_requests`},
	}
	for _, st := range steps {
		if _, err := tx.ExecContext(ctx, st.q); err != nil {
			return 0, fmt.Errorf("%s: %w", st.name, err)
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.companies`).Scan(&n); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
