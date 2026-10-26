package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// sameApplication: two acknowledgements this close together are one application
// (LinkedIn and the company often both confirm it).
const sameApplication = 48 * time.Hour

type candidate struct {
	id      int64
	key     string
	role    string
	applied time.Time
}

func candidates(ctx context.Context, tx *sql.Tx, userID, company string) ([]candidate, error) {
	// AppKey keeps only letters, digits and "|", so the LIKE pattern is safe.
	rows, err := tx.QueryContext(ctx, `SELECT id, key, role, applied_at FROM applications
		WHERE user_id = ? AND key LIKE ? ORDER BY applied_at DESC`, userID, AppKey(company, "")+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		var at string
		if err := rows.Scan(&c.id, &c.key, &c.role, &at); err != nil {
			return nil, err
		}
		c.applied, _ = time.Parse(time.RFC3339, at)
		out = append(out, c)
	}
	return out, rows.Err()
}

// similarRole: "Principal Product Manager" ~ "Principal Product Manager - Agentic AI".
func similarRole(a, b string) bool {
	trim := func(k string) string {
		for _, p := range []string{"theroleof", "roleof", "positionof", "postof"} {
			k = strings.TrimPrefix(k, p)
		}
		return k
	}
	ka, kb := trim(OrgKey(a)), trim(OrgKey(b))
	if len(ka) < 6 || len(kb) < 6 {
		return ka == kb
	}
	return strings.HasPrefix(ka, kb) || strings.HasPrefix(kb, ka)
}

func near(a, b time.Time) bool {
	d := a.Sub(b)
	return d < sameApplication && d > -sameApplication
}

// chooseKey decides which application an email belongs to:
//   - an email naming a role joins that role's application (upgrading a role-less
//     application from the same few days when there is one);
//   - a role-less "thank you for applying" starts a new application unless another
//     acknowledgement for the company arrived within 2 days;
//   - any other role-less email joins the company's latest earlier application.
func chooseKey(ctx context.Context, tx *sql.Tx, userID string, e IngestEvent) (string, error) {
	cands, err := candidates(ctx, tx, userID, e.Company)
	if err != nil {
		return "", err
	}
	at := e.OccurredAt
	if e.Role != "" {
		key := AppKey(e.Company, e.Role)
		for _, c := range cands {
			if c.key == key || (c.role != "" && similarRole(c.role, e.Role)) {
				return c.key, nil
			}
		}
		for _, c := range cands { // newest first
			if c.role == "" && (near(c.applied, at) || (e.Kind != "applied" && !c.applied.After(at.Add(sameApplication)))) {
				_, err := tx.ExecContext(ctx, `UPDATE applications SET key = ?, role = ? WHERE id = ?`, key, e.Role, c.id)
				return key, err
			}
		}
		return key, nil
	}
	if e.Kind == "applied" {
		for _, c := range cands {
			if near(c.applied, at) {
				return c.key, nil
			}
		}
		if len(cands) == 0 {
			return AppKey(e.Company, ""), nil
		}
		return AppKey(e.Company, "") + "@" + at.UTC().Format("20060102"), nil
	}
	for _, c := range cands {
		if !c.applied.After(at.Add(sameApplication)) {
			return c.key, nil
		}
	}
	if len(cands) > 0 {
		return cands[len(cands)-1].key, nil
	}
	return AppKey(e.Company, ""), nil
}

// SplitRolelessApplications separates applications that an earlier version merged:
// a role-less application holding several acknowledgements days apart becomes one
// application per acknowledgement. Other emails go to the latest earlier one.
// Safe to run on every start: an application with one acknowledgement is untouched.
func (s *Store) SplitRolelessApplications(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, user_id, key, company, source_account FROM applications WHERE role = ''`)
	if err != nil {
		return 0, err
	}
	type app struct {
		id                         int64
		user, key, company, source string
	}
	var apps []app
	for rows.Next() {
		var a app
		if err := rows.Scan(&a.id, &a.user, &a.key, &a.company, &a.source); err != nil {
			rows.Close()
			return 0, err
		}
		apps = append(apps, a)
	}
	rows.Close()

	created := 0
	for _, a := range apps {
		erows, err := tx.QueryContext(ctx, `SELECT id, occurred_at, kind FROM events WHERE application_id = ? ORDER BY occurred_at, id`, a.id)
		if err != nil {
			return 0, err
		}
		type ev struct {
			id   int64
			at   time.Time
			kind string
		}
		var evs []ev
		for erows.Next() {
			var e ev
			var at string
			if err := erows.Scan(&e.id, &at, &e.kind); err != nil {
				erows.Close()
				return 0, err
			}
			e.at, _ = time.Parse(time.RFC3339, at)
			evs = append(evs, e)
		}
		erows.Close()

		// Cluster starts: acknowledgements more than 2 days after the current cluster began.
		var starts []time.Time
		for _, e := range evs {
			if e.kind == "applied" && (len(starts) == 0 || e.at.Sub(starts[len(starts)-1]) >= sameApplication) {
				starts = append(starts, e.at)
			}
		}
		if len(starts) < 2 {
			continue
		}
		ids := []int64{a.id}
		for _, st := range starts[1:] {
			key := AppKey(a.company, "") + "@" + st.UTC().Format("20060102")
			var id int64
			if err := tx.QueryRowContext(ctx, `INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at)
				VALUES (?, ?, ?, '', 'applied', ?, ?, ?) ON CONFLICT (user_id, key) DO UPDATE SET key = excluded.key RETURNING id`,
				a.user, key, a.company, a.source, st.UTC().Format(time.RFC3339), st.UTC().Format(time.RFC3339)).Scan(&id); err != nil {
				return 0, err
			}
			ids = append(ids, id)
			created++
		}
		for _, e := range evs {
			target := ids[0]
			for i, st := range starts {
				if !e.at.Before(st) {
					target = ids[i]
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
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `UPDATE applications SET
				applied_at       = (SELECT MIN(occurred_at) FROM events WHERE application_id = ?),
				last_activity_at = (SELECT MAX(occurred_at) FROM events WHERE application_id = ?),
				stage = COALESCE((SELECT kind FROM events WHERE application_id = ? AND kind != 'update'
					ORDER BY occurred_at DESC, id DESC LIMIT 1), 'applied')
				WHERE id = ?`, id, id, id, id); err != nil {
				return 0, err
			}
			if err := linkOrganization(ctx, tx, a.user, id); err != nil {
				return 0, err
			}
		}
	}
	return created, tx.Commit()
}

// MergeOrphanApplications repairs applications made of only a reply (a rejection or
// interview with no "thank you for applying"), which happened when a mailbox returned
// newest mail first. Each joins the company's latest earlier application with a
// compatible role. Returns how many were merged.
func (s *Store) MergeOrphanApplications(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT a.id, a.user_id, a.company, a.role,
		(SELECT MIN(occurred_at) FROM events e WHERE e.application_id = a.id)
		FROM applications a
		WHERE NOT EXISTS (SELECT 1 FROM events e WHERE e.application_id = a.id AND e.kind = 'applied')
		  AND EXISTS (SELECT 1 FROM events e WHERE e.application_id = a.id)`)
	if err != nil {
		return 0, err
	}
	type orphan struct {
		id                  int64
		user, company, role string
		first               time.Time
	}
	var orphans []orphan
	for rows.Next() {
		var o orphan
		var first string
		if err := rows.Scan(&o.id, &o.user, &o.company, &o.role, &first); err != nil {
			rows.Close()
			return 0, err
		}
		o.first, _ = time.Parse(time.RFC3339, first)
		orphans = append(orphans, o)
	}
	rows.Close()

	merged := 0
	for _, o := range orphans {
		cands, err := candidates(ctx, tx, o.user, o.company)
		if err != nil {
			return 0, err
		}
		var target *candidate
		for i, c := range cands { // newest first
			if c.id == o.id || c.applied.After(o.first.Add(sameApplication)) {
				continue
			}
			if o.role == "" || c.role == "" || similarRole(c.role, o.role) {
				target = &cands[i]
				break
			}
		}
		if target == nil {
			continue
		}
		for _, q := range []string{
			`UPDATE events SET application_id = ? WHERE application_id = ?`,
			`UPDATE search_fts SET application_id = ? WHERE application_id = ?`,
			`UPDATE OR IGNORE contacts SET application_id = ? WHERE application_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, target.id, o.id); err != nil {
				return 0, err
			}
		}
		if target.role == "" && o.role != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE applications SET role = ?, key = ? WHERE id = ?`,
				o.role, AppKey(o.company, o.role), target.id); err != nil {
				return 0, err
			}
		}
		// The orphan is now empty; remove it and anything that pointed only at it.
		if _, err := tx.ExecContext(ctx, `DELETE FROM applications WHERE id = ?`, o.id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET
			applied_at       = (SELECT MIN(occurred_at) FROM events WHERE application_id = ?),
			last_activity_at = (SELECT MAX(occurred_at) FROM events WHERE application_id = ?),
			stage = COALESCE((SELECT kind FROM events WHERE application_id = ? AND kind != 'update'
				ORDER BY occurred_at DESC, id DESC LIMIT 1), stage)
			WHERE id = ?`, target.id, target.id, target.id, target.id); err != nil {
			return 0, err
		}
		merged++
	}
	return merged, tx.Commit()
}
