package store

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ImportFromCore copies the organizations and preferred list core-api held (before
// its migration v14 dropped those tables) into this service, keeping ids so links
// still point at the same organizations. Company research from that era moved on
// to the startoff service and isn't carried by this import (ADR 0011). It runs once, into an
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
	tables := []string{"organizations", "org_settings", "org_preferences"}
	for _, t := range tables {
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
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM main.organizations`).Scan(&n); err != nil {
		return 0, err
	}
	return n, tx.Commit()
}
