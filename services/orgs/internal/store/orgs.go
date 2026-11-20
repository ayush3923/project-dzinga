package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// OrgKey normalises a company name: "PhonePe Ltd." and "PHONEPE" share one organization.
func OrgKey(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ensureOrg(ctx context.Context, tx *sql.Tx, userID, name string) (int64, error) {
	name = strings.TrimSpace(name)
	key := OrgKey(name)
	if key == "" {
		return 0, errors.New("organization name needs letters or digits")
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO organizations (user_id, key, name, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, key) DO UPDATE SET key = excluded.key RETURNING id`,
		userID, key, truncate(name, 120), time.Now().UTC().Format(time.RFC3339)).Scan(&id)
	return id, err
}

// ---------- Report ----------

// App is an application as core-api's analysis reports it (only what the
// organization views need). Raw is passed through unchanged in a detail's history.
type App struct {
	ID        int64     `json:"id"`
	Company   string    `json:"company"`
	Stage     string    `json:"stage"`
	Open      bool      `json:"open"`
	AppliedAt time.Time `json:"applied_at"`
	Timeline  []struct {
		Stage string    `json:"stage"`
		At    time.Time `json:"at"`
	} `json:"timeline"`
	Raw json.RawMessage `json:"-"`
}

// DecodeApps reads core-api's analysis applications, keeping each one's JSON.
func DecodeApps(raw []json.RawMessage) ([]App, error) {
	out := make([]App, 0, len(raw))
	for _, r := range raw {
		var a App
		if err := json.Unmarshal(r, &a); err != nil {
			return nil, err
		}
		a.Raw = r
		out = append(out, a)
	}
	return out, nil
}

type OrgSummary struct {
	ID                 int64          `json:"id"`
	Name               string         `json:"name"`
	Preferred          bool           `json:"preferred"`
	Applications       int            `json:"applications"`
	Open               int            `json:"open"`
	Responded          int            `json:"responded"`
	Shortlisted        int            `json:"shortlisted"`
	Interviewed        int            `json:"interviewed"`
	Offers             int            `json:"offers"`
	Rejected           int            `json:"rejected"`
	StageCounts        map[string]int `json:"stage_counts"`
	FirstAppliedAt     *time.Time     `json:"first_applied_at"`
	LastActivityAt     *time.Time     `json:"last_activity_at"`
	AvgResponseDays    *float64       `json:"avg_response_days"`
	DiagnosisSuggested bool           `json:"diagnosis_suggested"`
	DiagnosisReason    string         `json:"diagnosis_reason"`
}

type OrgsReport struct {
	PreferredMode string       `json:"preferred_mode"` // auto | manual
	Preferred     []OrgSummary `json:"preferred"`
	Organizations []OrgSummary `json:"organizations"`
}

// summarize computes one organization's statistics from its applications.
func summarize(o *OrgSummary, apps []App, now time.Time) {
	o.StageCounts = map[string]int{}
	var waits []float64
	var oldest time.Time
	for _, a := range apps {
		o.Applications++
		o.StageCounts[a.Stage]++
		if a.Open {
			o.Open++
		}
		if len(a.Timeline) == 0 {
			continue
		}
		applied := a.Timeline[0].At
		if o.FirstAppliedAt == nil || applied.Before(*o.FirstAppliedAt) {
			t := applied
			o.FirstAppliedAt = &t
		}
		if oldest.IsZero() || applied.Before(oldest) {
			oldest = applied
		}
		last := a.Timeline[len(a.Timeline)-1].At
		if o.LastActivityAt == nil || last.After(*o.LastActivityAt) {
			t := last
			o.LastActivityAt = &t
		}
		responded, short, interview := false, false, false
		for _, st := range a.Timeline[1:] {
			switch st.Stage {
			case "shortlisted":
				short = true
			case "interview":
				interview = true
			case "offer":
				o.Offers++
			case "rejected":
				o.Rejected++
			}
			if !responded && (st.Stage == "shortlisted" || st.Stage == "offer" || st.Stage == "rejected") {
				responded = true
				waits = append(waits, st.At.Sub(applied).Hours()/24)
			}
		}
		if responded {
			o.Responded++
		}
		if short {
			o.Shortlisted++
		}
		if interview {
			o.Interviewed++
		}
	}
	if len(waits) > 0 {
		sum := 0.0
		for _, w := range waits {
			sum += w
		}
		avg := math.Round(sum/float64(len(waits))*10) / 10
		o.AvgResponseDays = &avg
	}
	// Diagnose when the company has gone silent or only ever rejected.
	switch {
	case o.Applications > 0 && o.Responded == 0 && !oldest.IsZero() && now.Sub(oldest) >= 7*24*time.Hour:
		o.DiagnosisSuggested = true
		o.DiagnosisReason = fmt.Sprintf("No response to %d %s", o.Applications, plural(o.Applications, "application", "applications"))
	case o.Rejected > 0 && o.Shortlisted == 0 && o.Offers == 0 && o.Rejected == o.Responded:
		o.DiagnosisSuggested = true
		o.DiagnosisReason = fmt.Sprintf("Every reply so far was a rejection (%d %s, %d %s)",
			o.Rejected, plural(o.Rejected, "rejection", "rejections"), o.Applications, plural(o.Applications, "application", "applications"))
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Organizations groups every application by company (OrgKey), newest activity first.
// An organization is created the first time an application to it is seen.
func (s *Store) Organizations(ctx context.Context, userID string, apps []App, now time.Time) (OrgsReport, error) {
	rep := OrgsReport{PreferredMode: "auto", Preferred: []OrgSummary{}, Organizations: []OrgSummary{}}
	byOrg, err := s.groupApps(ctx, userID, apps)
	if err != nil {
		return rep, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM organizations WHERE user_id = ?`, userID)
	if err != nil {
		return rep, err
	}
	all := map[int64]*OrgSummary{}
	for rows.Next() {
		o := &OrgSummary{}
		if err := rows.Scan(&o.ID, &o.Name); err != nil {
			rows.Close()
			return rep, err
		}
		summarize(o, byOrg[o.ID], now)
		all[o.ID] = o
	}
	rows.Close()

	var mode sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT preferred_mode FROM org_settings WHERE user_id = ?`, userID).Scan(&mode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return rep, err
	}
	var preferredIDs []int64
	if mode.String == "manual" {
		rep.PreferredMode = "manual"
		prows, err := s.db.QueryContext(ctx, `SELECT organization_id FROM org_preferences WHERE user_id = ? ORDER BY position`, userID)
		if err != nil {
			return rep, err
		}
		for prows.Next() {
			var id int64
			if err := prows.Scan(&id); err != nil {
				prows.Close()
				return rep, err
			}
			preferredIDs = append(preferredIDs, id)
		}
		prows.Close()
	} else {
		ranked := make([]*OrgSummary, 0, len(all))
		for _, o := range all {
			if o.Applications > 0 {
				ranked = append(ranked, o)
			}
		}
		sort.Slice(ranked, func(i, j int) bool {
			if ranked[i].Applications != ranked[j].Applications {
				return ranked[i].Applications > ranked[j].Applications
			}
			a, b := ranked[i].LastActivityAt, ranked[j].LastActivityAt
			return a != nil && (b == nil || a.After(*b))
		})
		for i := 0; i < len(ranked) && i < 3; i++ {
			preferredIDs = append(preferredIDs, ranked[i].ID)
		}
	}
	for _, id := range preferredIDs {
		if o, ok := all[id]; ok {
			o.Preferred = true
			rep.Preferred = append(rep.Preferred, *o)
		}
	}
	for _, o := range all {
		if o.Applications > 0 || o.Preferred {
			rep.Organizations = append(rep.Organizations, *o)
		}
	}
	sort.Slice(rep.Organizations, func(i, j int) bool {
		a, b := rep.Organizations[i], rep.Organizations[j]
		if (a.LastActivityAt == nil) != (b.LastActivityAt == nil) {
			return b.LastActivityAt == nil
		}
		if a.LastActivityAt != nil && !a.LastActivityAt.Equal(*b.LastActivityAt) {
			return a.LastActivityAt.After(*b.LastActivityAt)
		}
		return a.Name < b.Name
	})
	return rep, nil
}

// ---------- Preferences ----------

// SetPreferred replaces the user's preferred organizations (by name, in order),
// creating organizations for companies not applied to yet. An empty list with
// auto=true returns to "top 3 by applications".
func (s *Store) SetPreferred(ctx context.Context, userID string, names []string, auto bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM org_preferences WHERE user_id = ?`, userID); err != nil {
		return err
	}
	mode := "manual"
	if auto {
		mode = "auto"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO org_settings (user_id, preferred_mode) VALUES (?, ?)
		ON CONFLICT (user_id) DO UPDATE SET preferred_mode = excluded.preferred_mode`, userID, mode); err != nil {
		return err
	}
	if !auto {
		seen := map[string]bool{}
		pos := 0
		for _, n := range names {
			k := OrgKey(n)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			id, err := ensureOrg(ctx, tx, userID, n)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO org_preferences (user_id, organization_id, position) VALUES (?, ?, ?)`, userID, id, pos); err != nil {
				return err
			}
			pos++
		}
	}
	return tx.Commit()
}

func ownsOrg(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, userID string, orgID int64) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM organizations WHERE user_id = ? AND id = ?`, userID, orgID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type OrgDetail struct {
	OrgSummary
	History []json.RawMessage `json:"application_history"` // every application to this organization, as core-api reports it
}

func (s *Store) Organization(ctx context.Context, userID string, orgID int64, apps []App, now time.Time) (OrgDetail, error) {
	var d OrgDetail
	if err := ownsOrg(ctx, s.db, userID, orgID); err != nil {
		return d, err
	}
	rep, err := s.Organizations(ctx, userID, apps, now)
	if err != nil {
		return d, err
	}
	for _, o := range rep.Organizations {
		if o.ID == orgID {
			d.OrgSummary = o
		}
	}
	if d.ID == 0 { // an organization with no applications that isn't preferred
		if err := s.db.QueryRowContext(ctx, `SELECT id, name FROM organizations WHERE id = ?`, orgID).Scan(&d.ID, &d.Name); err != nil {
			return d, err
		}
		summarize(&d.OrgSummary, nil, now)
	}
	byOrg, err := s.groupApps(ctx, userID, apps)
	if err != nil {
		return d, err
	}
	mine := byOrg[orgID]
	sort.Slice(mine, func(i, j int) bool { return mine[i].AppliedAt.After(mine[j].AppliedAt) })
	d.History = []json.RawMessage{}
	for _, a := range mine {
		d.History = append(d.History, a.Raw)
	}
	return d, nil
}

func (s *Store) queryRows(ctx context.Context, q string, args []any, each func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) scanRows(ctx context.Context, q string, arg any, each func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, q, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// groupApps maps each application to its organization, creating organizations
// for companies seen for the first time.
func (s *Store) groupApps(ctx context.Context, userID string, apps []App) (map[int64][]App, error) {
	ids := map[string]int64{}
	err := s.queryRows(ctx, `SELECT key, id FROM organizations WHERE user_id = ?`, []any{userID}, func(r *sql.Rows) error {
		var k string
		var id int64
		err := r.Scan(&k, &id)
		ids[k] = id
		return err
	})
	if err != nil {
		return nil, err
	}
	out := map[int64][]App{}
	var tx *sql.Tx
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	for _, a := range apps {
		k := OrgKey(a.Company)
		if k == "" {
			continue
		}
		id, ok := ids[k]
		if !ok {
			if tx == nil {
				if tx, err = s.db.BeginTx(ctx, nil); err != nil {
					return nil, err
				}
			}
			if id, err = ensureOrg(ctx, tx, userID, a.Company); err != nil {
				return nil, err
			}
			ids[k] = id
		}
		out[id] = append(out[id], a)
	}
	if tx != nil {
		err = tx.Commit()
		tx = nil
	}
	return out, err
}

// OrgID finds an organization by company name (ErrNotFound when there is none).
func (s *Store) OrgID(ctx context.Context, userID, company string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM organizations WHERE user_id = ? AND key = ?`, userID, OrgKey(company)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
