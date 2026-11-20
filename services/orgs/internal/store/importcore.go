package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ImportFromCore copies everything core-api held about organizations (before its
// migration v14 dropped those tables) into this service, keeping ids so links and
// queued requests still point at the same organizations. It runs once, into an
// empty database, and reads the source read-only (a backup file works).
func (s *Store) ImportFromCore(ctx context.Context, corePath string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM organizations`).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		return 0, errors.New("the organizations database is not empty; import only runs once")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	src := (&url.URL{Scheme: "file", Path: corePath, RawQuery: "mode=ro"}).String()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS core`, src); err != nil {
		return 0, err
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE core`)

	has := func(table string) (bool, error) {
		var c int
		err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM core.sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&c)
		return c > 0, err
	}
	if ok, err := has("organizations"); err != nil || !ok {
		return 0, fmt.Errorf("%s has no organizations table (was core-api's migration v14 already run? use a backup): %v", corePath, err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	// core-api's columns, which this service's tables start with (later migrations
	// only add columns); parents before children.
	tables := []string{"organizations", "org_settings", "org_preferences", "company_profiles", "company_news", "company_products",
		"company_leaders", "company_ratings", "org_diagnoses", "org_diagnosis_items", "org_briefings", "org_briefing_news"}
	for _, t := range tables {
		ok, err := has(t)
		if err != nil {
			return 0, err
		}
		if !ok { // briefings arrived in core-api's v13
			continue
		}
		var cols []string
		rows, err := tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?, 'core') ORDER BY cid`, t)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				rows.Close()
				return 0, err
			}
			cols = append(cols, `"`+c+`"`)
		}
		rows.Close()
		list := strings.Join(cols, ", ")
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`INSERT INTO main.%s (%s) SELECT %s FROM core.%s`, t, list, list, t)); err != nil {
			return 0, fmt.Errorf("%s: %w", t, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO main.research_requests (user_id, organization_id, status, created_at, done_at)
		SELECT user_id, target_id, status, created_at, done_at FROM core.research_requests
		WHERE kind = 'company_profile' AND target_id IN (SELECT id FROM main.organizations)`); err != nil {
		return 0, fmt.Errorf("research_requests: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.organizations`).Scan(&n); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
