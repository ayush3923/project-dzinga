package store

import (
	"context"
	"database/sql"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Requisition ids. Two applications to the same title at one company ("Staff,
// Product Manager" twice at Walmart) differ only by the id in the email body, which
// the sync service extracts (classify.JobRef). An application with an id has the key
// "company|role#ID"; emails without one join the role's latest application by then.

var validRef = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

func normRef(ref string) string {
	if !validRef.MatchString(ref) {
		return ""
	}
	return strings.ToUpper(ref)
}

// baseKey drops a key's requisition id: "walmart|staffproductmanager#R-1" → "walmart|staffproductmanager".
func baseKey(key string) string {
	if i := strings.Index(key, "#"); i >= 0 {
		return key[:i]
	}
	return key
}

// keyForRef finds the application for a requisition id: the one keyed with it, else a
// same-role application that has no id yet (its first email, or mail from before ids
// were read), which takes the id. Otherwise the id starts a new application.
func keyForRef(ctx context.Context, tx *sql.Tx, cands []candidate, key, ref string, sameRole func(candidate) bool) (string, error) {
	rkey := key + "#" + ref
	for _, c := range cands {
		if c.key == rkey {
			return rkey, nil
		}
	}
	for _, c := range cands {
		if !sameRole(c) || strings.Contains(c.key, "#") {
			continue
		}
		var other int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE application_id = ? AND job_ref NOT IN ('', ?)`, c.id, ref).Scan(&other); err != nil {
			return "", err
		}
		if other == 0 {
			_, err := tx.ExecContext(ctx, `UPDATE applications SET key = ? WHERE id = ?`, rkey, c.id)
			return rkey, err
		}
	}
	return rkey, nil
}

// SplitByJobRef separates applications holding emails for different requisition ids
// (merged before ids were read, then filled in by a later sync). The earliest id keeps
// the application, with its fit score, posting and notes; each other id gets its own.
// Emails without an id go to the latest of them that began by then. Safe to run on
// every start. Returns how many applications were created.
func (s *Store) SplitByJobRef(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	type app struct {
		id                               int64
		user, key, company, role, source string
	}
	var apps []app
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.user_id, a.key, a.company, a.role, a.source_account FROM applications a
		WHERE (SELECT COUNT(DISTINCT job_ref) FROM events e WHERE e.application_id = a.id AND e.job_ref <> '') > 1`)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var a app
		if err := rows.Scan(&a.id, &a.user, &a.key, &a.company, &a.role, &a.source); err != nil {
			rows.Close()
			return 0, err
		}
		apps = append(apps, a)
	}
	rows.Close()

	created := 0
	for _, a := range apps {
		type ev struct {
			id  int64
			at  time.Time
			ref string
		}
		var evs []ev
		erows, err := tx.QueryContext(ctx, `SELECT id, occurred_at, job_ref FROM events WHERE application_id = ? ORDER BY occurred_at, id`, a.id)
		if err != nil {
			return 0, err
		}
		for erows.Next() {
			var e ev
			var at string
			if err := erows.Scan(&e.id, &at, &e.ref); err != nil {
				erows.Close()
				return 0, err
			}
			e.at, _ = time.Parse(time.RFC3339, at)
			evs = append(evs, e)
		}
		erows.Close()

		// Each id starts when its first email arrived; the earliest keeps this application.
		type group struct {
			ref   string
			start time.Time
			id    int64
		}
		var groups []*group
		byRef := map[string]*group{}
		for _, e := range evs {
			if e.ref != "" && byRef[e.ref] == nil {
				g := &group{ref: e.ref, start: e.at}
				byRef[e.ref] = g
				groups = append(groups, g)
			}
		}
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].start.Before(groups[j].start) })
		base := baseKey(a.key)
		groups[0].id = a.id
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET key = ? WHERE id = ?`, base+"#"+groups[0].ref, a.id); err != nil {
			return 0, err
		}
		for _, g := range groups[1:] {
			at := g.start.UTC().Format(time.RFC3339)
			if err := tx.QueryRowContext(ctx, `INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at)
				VALUES (?, ?, ?, ?, 'applied', ?, ?, ?) ON CONFLICT (user_id, key) DO UPDATE SET key = excluded.key RETURNING id`,
				a.user, base+"#"+g.ref, a.company, a.role, a.source, at, at).Scan(&g.id); err != nil {
				return 0, err
			}
			created++
		}
		for _, e := range evs {
			target := groups[0].id
			if g := byRef[e.ref]; g != nil {
				target = g.id
			} else {
				for _, g := range groups {
					if !e.at.Before(g.start) {
						target = g.id
					}
				}
			}
			if target == a.id {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE events SET application_id = ? WHERE id = ?`, target, e.id); err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE search_fts SET application_id = ? WHERE rowid = ?`, target, e.id); err != nil {
				return 0, err
			}
		}
		for _, g := range groups {
			if _, err := tx.ExecContext(ctx, `UPDATE applications SET
				applied_at       = (SELECT MIN(occurred_at) FROM events WHERE application_id = ?),
				last_activity_at = (SELECT MAX(occurred_at) FROM events WHERE application_id = ?),
				stage = COALESCE((SELECT kind FROM events WHERE application_id = ? AND kind != 'update'
					ORDER BY occurred_at DESC, id DESC LIMIT 1), 'applied')
				WHERE id = ?`, g.id, g.id, g.id, g.id); err != nil {
				return 0, err
			}
		}
	}
	return created, tx.Commit()
}
