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
	HasProfile         bool           `json:"has_profile"`
	HasDiagnosis       bool           `json:"has_diagnosis"`
	// Something new from the daily quiz (news, leaders, revenue) the user hasn't opened yet.
	BriefingUnseen bool `json:"briefing_unseen"`
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

	rows, err := s.db.QueryContext(ctx, `SELECT o.id, o.name,
		EXISTS (SELECT 1 FROM company_profiles p WHERE p.organization_id = o.id),
		EXISTS (SELECT 1 FROM org_diagnoses d WHERE d.organization_id = o.id),
		EXISTS (SELECT 1 FROM org_briefings b WHERE b.organization_id = o.id AND (b.seen_at IS NULL OR b.seen_at < b.updated_at))
		FROM organizations o WHERE o.user_id = ?`, userID)
	if err != nil {
		return rep, err
	}
	all := map[int64]*OrgSummary{}
	for rows.Next() {
		o := &OrgSummary{}
		if err := rows.Scan(&o.ID, &o.Name, &o.HasProfile, &o.HasDiagnosis, &o.BriefingUnseen); err != nil {
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

// ---------- Company profile and diagnosis ----------

type NewsItem struct {
	Title       string `json:"title"`
	PublishedOn string `json:"published_on"`
	Summary     string `json:"summary"`
	URL         string `json:"url"`
}

type Product struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Leader struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type Rating struct {
	Source      string   `json:"source"` // glassdoor | gartner_peer_insights
	Score       *float64 `json:"score"`  // nil when not found
	Scale       float64  `json:"scale"`
	ReviewCount *int     `json:"review_count"`
	URL         string   `json:"url"`
	AsOf        string   `json:"as_of"`
}

type CompanyProfile struct {
	Overview       string     `json:"overview"`
	InterviewAngle string     `json:"interview_angle"`
	News           []NewsItem `json:"news"`
	Products       []Product  `json:"products"`
	Leaders        []Leader   `json:"leaders"`
	Ratings        []Rating   `json:"ratings"`
	Model          string     `json:"model"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (p CompanyProfile) Validate() error {
	if strings.TrimSpace(p.Overview) == "" || p.Model == "" {
		return errors.New("overview and model are required")
	}
	if len(p.News) > 5 || len(p.Products) > 12 || len(p.Leaders) > 12 || len(p.Ratings) > 2 {
		return errors.New("too many items (news ≤ 5, products ≤ 12, leaders ≤ 12, ratings ≤ 2)")
	}
	for _, n := range p.News {
		if n.URL != "" && !strings.HasPrefix(n.URL, "https://") {
			return errors.New("news links must be https")
		}
	}
	for _, r := range p.Ratings {
		if r.Source != "glassdoor" && r.Source != "gartner_peer_insights" {
			return fmt.Errorf("invalid rating source %q", r.Source)
		}
		if r.Score != nil && (*r.Score < 0 || r.Scale <= 0 || *r.Score > r.Scale) {
			return errors.New("rating out of range")
		}
		if r.URL != "" && !strings.HasPrefix(r.URL, "https://") {
			return errors.New("rating links must be https")
		}
	}
	return nil
}

type DiagnosisItem struct {
	Text       string `json:"text"`
	Confidence string `json:"confidence"` // high | medium | low
}

type Diagnosis struct {
	Summary   string          `json:"summary"`
	Causes    []DiagnosisItem `json:"causes"`
	Actions   []string        `json:"actions"`
	Model     string          `json:"model"`
	CreatedAt time.Time       `json:"created_at"`
}

func (d Diagnosis) Validate() error {
	if strings.TrimSpace(d.Summary) == "" || d.Model == "" {
		return errors.New("summary and model are required")
	}
	if len(d.Causes) > 8 || len(d.Actions) > 8 {
		return errors.New("too many causes or actions")
	}
	for _, c := range d.Causes {
		if c.Confidence != "high" && c.Confidence != "medium" && c.Confidence != "low" {
			return fmt.Errorf("invalid confidence %q", c.Confidence)
		}
	}
	return nil
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

func (s *Store) SaveCompanyProfile(ctx context.Context, userID string, orgID int64, p CompanyProfile, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownsOrg(ctx, tx, userID, orgID); err != nil {
		return err
	}
	if err := markResearchDone(ctx, tx, userID, orgID, now); err != nil {
		return err
	}
	for _, q := range []string{`DELETE FROM company_news WHERE organization_id = ?`, `DELETE FROM company_products WHERE organization_id = ?`,
		`DELETE FROM company_leaders WHERE organization_id = ?`, `DELETE FROM company_ratings WHERE organization_id = ?`} {
		if _, err := tx.ExecContext(ctx, q, orgID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO company_profiles (organization_id, user_id, overview, interview_angle, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (organization_id) DO UPDATE SET overview = excluded.overview,
		interview_angle = excluded.interview_angle, model = excluded.model, created_at = excluded.created_at`,
		orgID, userID, truncate(p.Overview, 3000), truncate(p.InterviewAngle, 3000), truncate(p.Model, 100), now.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	for i, n := range p.News {
		if _, err := tx.ExecContext(ctx, `INSERT INTO company_news (organization_id, position, title, published_on, summary, url) VALUES (?, ?, ?, ?, ?, ?)`,
			orgID, i, truncate(n.Title, 300), truncate(n.PublishedOn, 10), truncate(n.Summary, 800), truncate(n.URL, 2000)); err != nil {
			return err
		}
	}
	for i, pr := range p.Products {
		if _, err := tx.ExecContext(ctx, `INSERT INTO company_products (organization_id, position, name, description) VALUES (?, ?, ?, ?)`,
			orgID, i, truncate(pr.Name, 150), truncate(pr.Description, 600)); err != nil {
			return err
		}
	}
	for i, l := range p.Leaders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO company_leaders (organization_id, position, name, title) VALUES (?, ?, ?, ?)`,
			orgID, i, truncate(l.Name, 120), truncate(l.Title, 160)); err != nil {
			return err
		}
	}
	for _, r := range p.Ratings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO company_ratings (organization_id, source, score, scale, review_count, url, as_of) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			orgID, r.Source, r.Score, r.Scale, r.ReviewCount, truncate(r.URL, 2000), truncate(r.AsOf, 10)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveDiagnosis(ctx context.Context, userID string, orgID int64, d Diagnosis, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownsOrg(ctx, tx, userID, orgID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO org_diagnoses (user_id, organization_id, summary, model, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, orgID, truncate(d.Summary, 2000), truncate(d.Model, 100), now.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	did, _ := res.LastInsertId()
	for i, c := range d.Causes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO org_diagnosis_items (diagnosis_id, kind, position, text, confidence) VALUES (?, 'cause', ?, ?, ?)`,
			did, i, truncate(c.Text, 800), c.Confidence); err != nil {
			return err
		}
	}
	for i, a := range d.Actions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO org_diagnosis_items (diagnosis_id, kind, position, text) VALUES (?, 'action', ?, ?)`,
			did, i, truncate(a, 800)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type OrgDetail struct {
	OrgSummary
	History   []json.RawMessage `json:"application_history"` // every application to this organization, as core-api reports it
	Profile   *CompanyProfile   `json:"profile"`
	Diagnosis *Diagnosis        `json:"diagnosis"`
	Briefing  *Briefing         `json:"briefing"` // from the daily quiz (nil when none)
	// When the user queued company research for Claude in the app (nil when not queued).
	ResearchRequestedAt *time.Time `json:"research_requested_at"`
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
	if d.ResearchRequestedAt, err = s.researchRequestedAt(ctx, userID, orgID); err != nil {
		return d, err
	}
	if d.Briefing, err = s.briefing(ctx, orgID); err != nil {
		return d, err
	}

	var p CompanyProfile
	var created string
	err = s.db.QueryRowContext(ctx, `SELECT overview, interview_angle, model, created_at FROM company_profiles WHERE organization_id = ?`, orgID).
		Scan(&p.Overview, &p.InterviewAngle, &p.Model, &created)
	if err == nil {
		p.CreatedAt, _ = time.Parse(time.RFC3339, created)
		p.News, p.Products, p.Leaders, p.Ratings = []NewsItem{}, []Product{}, []Leader{}, []Rating{}
		if err := s.scanRows(ctx, `SELECT title, published_on, summary, url FROM company_news WHERE organization_id = ? ORDER BY position`, orgID,
			func(r *sql.Rows) error {
				var n NewsItem
				err := r.Scan(&n.Title, &n.PublishedOn, &n.Summary, &n.URL)
				p.News = append(p.News, n)
				return err
			}); err != nil {
			return d, err
		}
		if err := s.scanRows(ctx, `SELECT name, description FROM company_products WHERE organization_id = ? ORDER BY position`, orgID,
			func(r *sql.Rows) error {
				var x Product
				err := r.Scan(&x.Name, &x.Description)
				p.Products = append(p.Products, x)
				return err
			}); err != nil {
			return d, err
		}
		if err := s.scanRows(ctx, `SELECT name, title FROM company_leaders WHERE organization_id = ? ORDER BY position`, orgID,
			func(r *sql.Rows) error {
				var x Leader
				err := r.Scan(&x.Name, &x.Title)
				p.Leaders = append(p.Leaders, x)
				return err
			}); err != nil {
			return d, err
		}
		if err := s.scanRows(ctx, `SELECT source, score, scale, review_count, url, as_of FROM company_ratings WHERE organization_id = ? ORDER BY source DESC`, orgID,
			func(r *sql.Rows) error {
				var x Rating
				var score sql.NullFloat64
				var count sql.NullInt64
				err := r.Scan(&x.Source, &score, &x.Scale, &count, &x.URL, &x.AsOf)
				if score.Valid {
					x.Score = &score.Float64
				}
				if count.Valid {
					c := int(count.Int64)
					x.ReviewCount = &c
				}
				p.Ratings = append(p.Ratings, x)
				return err
			}); err != nil {
			return d, err
		}
		d.Profile = &p
	} else if !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}

	var dg Diagnosis
	var did int64
	err = s.db.QueryRowContext(ctx, `SELECT id, summary, model, created_at FROM org_diagnoses WHERE organization_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, orgID).
		Scan(&did, &dg.Summary, &dg.Model, &created)
	if err == nil {
		dg.CreatedAt, _ = time.Parse(time.RFC3339, created)
		dg.Causes, dg.Actions = []DiagnosisItem{}, []string{}
		if err := s.scanRows(ctx, `SELECT kind, text, confidence FROM org_diagnosis_items WHERE diagnosis_id = ? ORDER BY kind, position`, did,
			func(r *sql.Rows) error {
				var kind, text, conf string
				err := r.Scan(&kind, &text, &conf)
				if kind == "cause" {
					dg.Causes = append(dg.Causes, DiagnosisItem{Text: text, Confidence: conf})
				} else {
					dg.Actions = append(dg.Actions, text)
				}
				return err
			}); err != nil {
			return d, err
		}
		d.Diagnosis = &dg
	} else if !errors.Is(err, sql.ErrNoRows) {
		return d, err
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
