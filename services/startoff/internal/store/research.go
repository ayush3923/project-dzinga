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

// Company research, written by Claude in the app (ADR 0007) and kept with its
// history: a new profile is a new version, quiz facts are a new row when they
// change, quiz news is added (each link once), and every diagnosis is kept. The
// newest of each is what the organization drawer shows.

// Org is an organization as the organizations service knows it.
type Org struct {
	ID   int64
	Name string
}

var reDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func httpsOrEmpty(u string) bool {
	return u == "" || (strings.HasPrefix(u, "https://") && len(u) <= 2000)
}

// ---------- Company profile ----------

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
		if !httpsOrEmpty(n.URL) {
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
		if !httpsOrEmpty(r.URL) {
			return errors.New("rating links must be https")
		}
	}
	return nil
}

// SaveProfile adds a profile version and completes a queued profile request.
func (s *Store) SaveProfile(ctx context.Context, userID string, org Org, p CompanyProfile, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureCompany(ctx, tx, userID, org); err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339)
	res, err := tx.ExecContext(ctx, `INSERT INTO profiles (user_id, org_id, overview, interview_angle, model, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		userID, org.ID, truncate(p.Overview, 3000), truncate(p.InterviewAngle, 3000), truncate(p.Model, 100), at)
	if err != nil {
		return err
	}
	pid, _ := res.LastInsertId()
	for i, n := range p.News {
		if _, err := tx.ExecContext(ctx, `INSERT INTO news (user_id, org_id, origin, profile_id, position, title, published_on, summary, url, created_at)
			VALUES (?, ?, 'profile', ?, ?, ?, ?, ?, ?, ?)`,
			userID, org.ID, pid, i, truncate(n.Title, 300), truncate(n.PublishedOn, 10), truncate(n.Summary, 800), truncate(n.URL, 2000), at); err != nil {
			return err
		}
	}
	for i, pr := range p.Products {
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_products (profile_id, position, name, description) VALUES (?, ?, ?, ?)`,
			pid, i, truncate(pr.Name, 150), truncate(pr.Description, 600)); err != nil {
			return err
		}
	}
	for i, l := range p.Leaders {
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_leaders (profile_id, position, name, title) VALUES (?, ?, ?, ?)`,
			pid, i, truncate(l.Name, 120), truncate(l.Title, 160)); err != nil {
			return err
		}
	}
	for _, r := range p.Ratings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO profile_ratings (profile_id, source, score, scale, review_count, url, as_of) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			pid, r.Source, r.Score, r.Scale, r.ReviewCount, truncate(r.URL, 2000), truncate(r.AsOf, 10)); err != nil {
			return err
		}
	}
	if err := markDone(ctx, tx, userID, KindProfile, org.ID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// profiles reads an organization's profile versions, newest first (limit 0 = all).
func (s *Store) profiles(ctx context.Context, userID string, orgID int64, limit int) ([]CompanyProfile, error) {
	type row struct {
		id int64
		p  CompanyProfile
	}
	var rows []row
	q := `SELECT id, overview, interview_angle, model, created_at FROM profiles WHERE user_id = ? AND org_id = ? ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	err := s.queryRows(ctx, q, []any{userID, orgID}, func(r *sql.Rows) error {
		var x row
		var created string
		err := r.Scan(&x.id, &x.p.Overview, &x.p.InterviewAngle, &x.p.Model, &created)
		x.p.CreatedAt, _ = time.Parse(time.RFC3339, created)
		rows = append(rows, x)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]CompanyProfile, 0, len(rows))
	for _, x := range rows {
		p := x.p
		p.News, p.Products, p.Leaders, p.Ratings = []NewsItem{}, []Product{}, []Leader{}, []Rating{}
		if err := s.queryRows(ctx, `SELECT title, published_on, summary, url FROM news WHERE profile_id = ? ORDER BY position`, []any{x.id},
			func(r *sql.Rows) error {
				var n NewsItem
				err := r.Scan(&n.Title, &n.PublishedOn, &n.Summary, &n.URL)
				p.News = append(p.News, n)
				return err
			}); err != nil {
			return nil, err
		}
		if err := s.queryRows(ctx, `SELECT name, description FROM profile_products WHERE profile_id = ? ORDER BY position`, []any{x.id},
			func(r *sql.Rows) error {
				var v Product
				err := r.Scan(&v.Name, &v.Description)
				p.Products = append(p.Products, v)
				return err
			}); err != nil {
			return nil, err
		}
		if err := s.queryRows(ctx, `SELECT name, title FROM profile_leaders WHERE profile_id = ? ORDER BY position`, []any{x.id},
			func(r *sql.Rows) error {
				var v Leader
				err := r.Scan(&v.Name, &v.Title)
				p.Leaders = append(p.Leaders, v)
				return err
			}); err != nil {
			return nil, err
		}
		if err := s.queryRows(ctx, `SELECT source, score, scale, review_count, url, as_of FROM profile_ratings WHERE profile_id = ? ORDER BY id`, []any{x.id},
			func(r *sql.Rows) error {
				var v Rating
				var score sql.NullFloat64
				var count sql.NullInt64
				err := r.Scan(&v.Source, &score, &v.Scale, &count, &v.URL, &v.AsOf)
				if score.Valid {
					v.Score = &score.Float64
				}
				if count.Valid {
					c := int(count.Int64)
					v.ReviewCount = &c
				}
				p.Ratings = append(p.Ratings, v)
				return err
			}); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ---------- Briefing: facts and news from the daily quiz ----------

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

// briefingNewsShown is how many quiz news items the drawer shows; all are kept.
const briefingNewsShown = 10

// SaveBriefing records the quiz's facts (a new row only when they changed) and adds
// its news (a link already saved is kept as it was). Either flags the organization
// until the user opens it.
func (s *Store) SaveBriefing(ctx context.Context, userID string, org Org, b Briefing, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureCompany(ctx, tx, userID, org); err != nil {
		return err
	}
	at := now.UTC().Format(time.RFC3339)
	vals := []any{strings.TrimSpace(b.CEO), strings.TrimSpace(b.CPO),
		strings.TrimSpace(b.RevenueYear.Value), strings.TrimSpace(b.RevenueYear.Period), b.RevenueYear.URL,
		strings.TrimSpace(b.RevenueQuarter.Value), strings.TrimSpace(b.RevenueQuarter.Period), b.RevenueQuarter.URL, b.LeadersURL}
	var same int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT * FROM facts WHERE user_id = ? AND org_id = ? ORDER BY created_at DESC, id DESC LIMIT 1)
		WHERE ceo = ? AND cpo = ? AND revenue_year = ? AND revenue_year_period = ? AND revenue_year_url = ?
		  AND revenue_quarter = ? AND revenue_quarter_period = ? AND revenue_quarter_url = ? AND leaders_url = ?`,
		append([]any{userID, org.ID}, vals...)...).Scan(&same)
	if err != nil {
		return err
	}
	if same == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO facts (user_id, org_id, ceo, cpo, revenue_year, revenue_year_period, revenue_year_url,
				revenue_quarter, revenue_quarter_period, revenue_quarter_url, leaders_url, model, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append(append([]any{userID, org.ID}, vals...), truncate(b.Model, 100), at)...); err != nil {
			return err
		}
	}
	for _, n := range b.News {
		if _, err := tx.ExecContext(ctx, `INSERT INTO news (user_id, org_id, origin, day, title, summary, source_title, url, published_on, created_at)
			VALUES (?, ?, 'quiz', ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			userID, org.ID, n.Day, strings.TrimSpace(n.Title), strings.TrimSpace(n.Summary), truncate(strings.TrimSpace(n.SourceTitle), 120),
			n.URL, n.PublishedOn, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// facts reads the facts snapshots, newest first (limit 0 = all), without news.
func (s *Store) facts(ctx context.Context, userID string, orgID int64, limit int) ([]Briefing, error) {
	out := []Briefing{}
	q := `SELECT ceo, cpo, revenue_year, revenue_year_period, revenue_year_url, revenue_quarter, revenue_quarter_period,
		revenue_quarter_url, leaders_url, model, created_at FROM facts WHERE user_id = ? AND org_id = ? ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	err := s.queryRows(ctx, q, []any{userID, orgID}, func(r *sql.Rows) error {
		var b Briefing
		var created string
		err := r.Scan(&b.CEO, &b.CPO, &b.RevenueYear.Value, &b.RevenueYear.Period, &b.RevenueYear.URL,
			&b.RevenueQuarter.Value, &b.RevenueQuarter.Period, &b.RevenueQuarter.URL, &b.LeadersURL, &b.Model, &created)
		b.UpdatedAt, _ = time.Parse(time.RFC3339, created)
		b.News = []BriefingNews{}
		out = append(out, b)
		return err
	})
	return out, err
}

// quizNews reads news the quiz found, newest first (limit 0 = all), and when the newest arrived.
func (s *Store) quizNews(ctx context.Context, userID string, orgID int64, limit int) ([]BriefingNews, time.Time, error) {
	out := []BriefingNews{}
	var newest time.Time
	q := `SELECT day, title, summary, source_title, url, published_on, created_at FROM news
		WHERE user_id = ? AND org_id = ? AND origin = 'quiz' ORDER BY COALESCE(NULLIF(published_on, ''), day) DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	err := s.queryRows(ctx, q, []any{userID, orgID}, func(r *sql.Rows) error {
		var n BriefingNews
		var created string
		err := r.Scan(&n.Day, &n.Title, &n.Summary, &n.SourceTitle, &n.URL, &n.PublishedOn, &created)
		if t, _ := time.Parse(time.RFC3339, created); t.After(newest) {
			newest = t
		}
		out = append(out, n)
		return err
	})
	return out, newest, err
}

// ---------- Diagnosis ----------

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

// SaveDiagnosis adds a diagnosis and completes a queued one; the organization is
// flagged until the user opens it.
func (s *Store) SaveDiagnosis(ctx context.Context, userID string, org Org, d Diagnosis, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ensureCompany(ctx, tx, userID, org); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO diagnoses (user_id, org_id, summary, model, created_at) VALUES (?, ?, ?, ?, ?)`,
		userID, org.ID, truncate(d.Summary, 2000), truncate(d.Model, 100), now.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	did, _ := res.LastInsertId()
	for i, c := range d.Causes {
		if _, err := tx.ExecContext(ctx, `INSERT INTO diagnosis_items (diagnosis_id, kind, position, text, confidence) VALUES (?, 'cause', ?, ?, ?)`,
			did, i, truncate(c.Text, 800), c.Confidence); err != nil {
			return err
		}
	}
	for i, a := range d.Actions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO diagnosis_items (diagnosis_id, kind, position, text) VALUES (?, 'action', ?, ?)`,
			did, i, truncate(a, 800)); err != nil {
			return err
		}
	}
	if err := markDone(ctx, tx, userID, KindDiagnosis, org.ID, now); err != nil {
		return err
	}
	return tx.Commit()
}

// diagnoses reads diagnoses, newest first (limit 0 = all).
func (s *Store) diagnoses(ctx context.Context, userID string, orgID int64, limit int) ([]Diagnosis, error) {
	type row struct {
		id int64
		d  Diagnosis
	}
	var rows []row
	q := `SELECT id, summary, model, created_at FROM diagnoses WHERE user_id = ? AND org_id = ? ORDER BY created_at DESC, id DESC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	err := s.queryRows(ctx, q, []any{userID, orgID}, func(r *sql.Rows) error {
		var x row
		var created string
		err := r.Scan(&x.id, &x.d.Summary, &x.d.Model, &created)
		x.d.CreatedAt, _ = time.Parse(time.RFC3339, created)
		rows = append(rows, x)
		return err
	})
	if err != nil {
		return nil, err
	}
	out := make([]Diagnosis, 0, len(rows))
	for _, x := range rows {
		d := x.d
		d.Causes, d.Actions = []DiagnosisItem{}, []string{}
		if err := s.queryRows(ctx, `SELECT kind, text, confidence FROM diagnosis_items WHERE diagnosis_id = ? ORDER BY kind, position`, []any{x.id},
			func(r *sql.Rows) error {
				var kind, text, conf string
				err := r.Scan(&kind, &text, &conf)
				if kind == "cause" {
					d.Causes = append(d.Causes, DiagnosisItem{Text: text, Confidence: conf})
				} else {
					d.Actions = append(d.Actions, text)
				}
				return err
			}); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// ---------- What the organization pages read ----------

// Flags are what the organizations list shows for one organization.
type Flags struct {
	OrgID        int64 `json:"organization_id"`
	HasProfile   bool  `json:"has_profile"`
	HasDiagnosis bool  `json:"has_diagnosis"`
	// Something new from the daily quiz (facts or news) since the user last opened it.
	BriefingUnseen bool `json:"briefing_unseen"`
	// A diagnosis written since the user last opened it.
	DiagnosisUnseen bool `json:"diagnosis_unseen"`
}

const flagsQuery = `SELECT c.org_id,
	EXISTS (SELECT 1 FROM profiles p WHERE p.user_id = c.user_id AND p.org_id = c.org_id),
	EXISTS (SELECT 1 FROM diagnoses d WHERE d.user_id = c.user_id AND d.org_id = c.org_id),
	MAX(COALESCE((SELECT MAX(created_at) FROM facts f WHERE f.user_id = c.user_id AND f.org_id = c.org_id), ''),
	    COALESCE((SELECT MAX(created_at) FROM news n WHERE n.user_id = c.user_id AND n.org_id = c.org_id AND n.origin = 'quiz'), ''))
	  > COALESCE(c.briefing_seen_at, ''),
	COALESCE((SELECT MAX(created_at) FROM diagnoses d WHERE d.user_id = c.user_id AND d.org_id = c.org_id), '') > COALESCE(c.diagnosis_seen_at, '')
	FROM companies c WHERE c.user_id = ?`

// AllFlags lists the flags for every organization with research.
func (s *Store) AllFlags(ctx context.Context, userID string) ([]Flags, error) {
	out := []Flags{}
	err := s.queryRows(ctx, flagsQuery+` ORDER BY c.org_id`, []any{userID}, func(r *sql.Rows) error {
		var f Flags
		err := r.Scan(&f.OrgID, &f.HasProfile, &f.HasDiagnosis, &f.BriefingUnseen, &f.DiagnosisUnseen)
		out = append(out, f)
		return err
	})
	return out, err
}

// Research is everything known about one organization: the newest of each kind,
// recent history and what is queued.
type Research struct {
	Flags
	Profile         *CompanyProfile `json:"profile"`
	ProfileVersions int             `json:"profile_versions"`
	// The newest facts with the newest quiz news (all news is kept: see History).
	Briefing  *Briefing  `json:"briefing"`
	NewsCount int        `json:"news_count"`
	Diagnosis *Diagnosis `json:"diagnosis"`
	// Earlier diagnoses, newest first (up to 10).
	EarlierDiagnoses     []Diagnosis `json:"earlier_diagnoses"`
	ResearchRequestedAt  *time.Time  `json:"research_requested_at"`
	DiagnosisRequestedAt *time.Time  `json:"diagnosis_requested_at"`
}

// OrgResearch reads one organization's research. An organization with none returns
// empty research, not an error.
func (s *Store) OrgResearch(ctx context.Context, userID string, orgID int64) (Research, error) {
	r := Research{Flags: Flags{OrgID: orgID}, EarlierDiagnoses: []Diagnosis{}}
	err := s.db.QueryRowContext(ctx, flagsQuery+` AND c.org_id = ?`, userID, orgID).
		Scan(&r.OrgID, &r.HasProfile, &r.HasDiagnosis, &r.BriefingUnseen, &r.DiagnosisUnseen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	if ps, err := s.profiles(ctx, userID, orgID, 1); err != nil {
		return r, err
	} else if len(ps) > 0 {
		r.Profile = &ps[0]
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM profiles WHERE user_id = ? AND org_id = ?`, userID, orgID).Scan(&r.ProfileVersions); err != nil {
		return r, err
	}
	fs, err := s.facts(ctx, userID, orgID, 1)
	if err != nil {
		return r, err
	}
	news, newest, err := s.quizNews(ctx, userID, orgID, briefingNewsShown)
	if err != nil {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM news WHERE user_id = ? AND org_id = ? AND origin = 'quiz'`, userID, orgID).Scan(&r.NewsCount); err != nil {
		return r, err
	}
	if len(fs) > 0 || len(news) > 0 {
		b := Briefing{News: news}
		if len(fs) > 0 {
			b = fs[0]
			b.News = news
		}
		if newest.After(b.UpdatedAt) {
			b.UpdatedAt = newest
		}
		r.Briefing = &b
	}
	ds, err := s.diagnoses(ctx, userID, orgID, 11)
	if err != nil {
		return r, err
	}
	if len(ds) > 0 {
		r.Diagnosis = &ds[0]
		r.EarlierDiagnoses = ds[1:]
	}
	if r.ResearchRequestedAt, err = s.requestedAt(ctx, userID, KindProfile, orgID); err != nil {
		return r, err
	}
	r.DiagnosisRequestedAt, err = s.requestedAt(ctx, userID, KindDiagnosis, orgID)
	return r, err
}

// History is everything ever saved for one organization, newest first.
type History struct {
	Profiles  []CompanyProfile `json:"profiles"`
	Facts     []Briefing       `json:"facts"`
	News      []BriefingNews   `json:"news"`
	Diagnoses []Diagnosis      `json:"diagnoses"`
}

func (s *Store) OrgHistory(ctx context.Context, userID string, orgID int64) (History, error) {
	var h History
	var err error
	if h.Profiles, err = s.profiles(ctx, userID, orgID, 0); err != nil {
		return h, err
	}
	if h.Facts, err = s.facts(ctx, userID, orgID, 0); err != nil {
		return h, err
	}
	if h.News, _, err = s.quizNews(ctx, userID, orgID, 0); err != nil {
		return h, err
	}
	h.Diagnoses, err = s.diagnoses(ctx, userID, orgID, 0)
	return h, err
}

// MarkSeen clears the organization's flags (the user opened it).
func (s *Store) MarkSeen(ctx context.Context, userID string, orgID int64, now time.Time) error {
	at := now.UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `UPDATE companies SET briefing_seen_at = ?, diagnosis_seen_at = ? WHERE user_id = ? AND org_id = ?`,
		at, at, userID, orgID)
	return err
}

func ensureCompany(ctx context.Context, tx *sql.Tx, userID string, org Org) error {
	if org.ID <= 0 || strings.TrimSpace(org.Name) == "" {
		return ErrNotFound
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO companies (user_id, org_id, name) VALUES (?, ?, ?)
		ON CONFLICT (user_id, org_id) DO UPDATE SET name = excluded.name`, userID, org.ID, truncate(org.Name, 200))
	return err
}
