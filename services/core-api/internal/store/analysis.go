package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// ---------- Report types (the Application analysis page) ----------

type TimelineStep struct {
	Stage        string    `json:"stage"`
	Round        int       `json:"round"`
	Label        string    `json:"label"`
	At           time.Time `json:"at"`
	DaysFromPrev *int      `json:"days_from_prev"`
	Source       string    `json:"source"`
}

type NextMove struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Urgent bool   `json:"urgent"`
}

type FitSummary struct {
	Total     int       `json:"total"`
	Verdict   string    `json:"verdict"`
	CreatedAt time.Time `json:"created_at"`
}

type AppAnalysis struct {
	Application
	Open           bool           `json:"open"`
	StageSince     time.Time      `json:"stage_since"`
	DaysInStage    int            `json:"days_in_stage"`
	NextMove       NextMove       `json:"next_move"`
	Timeline       []TimelineStep `json:"timeline"`
	Outcome        string         `json:"outcome"`          // offer | rejected | ""
	DaysToOutcome  *int           `json:"days_to_outcome"`  // applied → offer or rejection
	InterviewCount int            `json:"interview_rounds"` // estimated from invites
	Fit            *FitSummary    `json:"fit"`
	PostingURL     string         `json:"posting_url"`
}

type DurationStat struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	AvgDays    float64 `json:"avg_days"`
	MedianDays float64 `json:"median_days"`
	N          int     `json:"n"`
}

type Funnel struct {
	Applied      int     `json:"applied"`
	Responded    int     `json:"responded"` // any reply: shortlisted, rejected or offer
	Shortlisted  int     `json:"shortlisted"`
	Interviewed  int     `json:"interviewed"`
	Offers       int     `json:"offers"`
	Rejected     int     `json:"rejected"`
	ResponseRate float64 `json:"response_rate"`
}

type AnalysisReport struct {
	Applications []AppAnalysis  `json:"applications"`
	StageDwell   []DurationStat `json:"stage_dwell"` // time spent in each stage before the next step
	Outcomes     []DurationStat `json:"outcomes"`    // applied → first response / rejection / offer
	Funnel       Funnel         `json:"funnel"`
}

func label(stage string, round int) string {
	switch stage {
	case "interview":
		return fmt.Sprintf("Interview %d", round)
	case "shortlisted":
		return "Shortlisted"
	case "rejected":
		return "Rejected"
	}
	return string(stage[0]-32) + stage[1:]
}

func dwellKey(s TimelineStep) (string, string) {
	if s.Stage == "interview" {
		if s.Round >= 3 {
			return "interview_3plus", "Interview 3+"
		}
		return fmt.Sprintf("interview_%d", s.Round), s.Label
	}
	return s.Stage, s.Label
}

func days(d time.Duration) int { return int(math.Floor(d.Hours() / 24)) }

func stat(key, lbl string, xs []float64) DurationStat {
	st := DurationStat{Key: key, Label: lbl, N: len(xs)}
	if len(xs) == 0 {
		return st
	}
	sort.Float64s(xs)
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	st.AvgDays = math.Round(sum/float64(len(xs))*10) / 10
	if len(xs)%2 == 1 {
		st.MedianDays = xs[len(xs)/2]
	} else {
		st.MedianDays = (xs[len(xs)/2-1] + xs[len(xs)/2]) / 2
	}
	return st
}

// Analysis builds the whole Application analysis report for one user.
func (s *Store) Analysis(ctx context.Context, userID string, now time.Time) (AnalysisReport, error) {
	var rep AnalysisReport
	apps, err := s.ListApplications(ctx, userID, ListFilter{})
	if err != nil {
		return rep, err
	}
	hist, err := s.histories(ctx, userID)
	if err != nil {
		return rep, err
	}
	fits, err := s.latestFits(ctx, userID)
	if err != nil {
		return rep, err
	}
	urls, err := s.postingURLs(ctx, userID)
	if err != nil {
		return rep, err
	}

	dwell := map[string][]float64{}
	dwellLabel := map[string]string{}
	var toResponse, toReject, toOffer []float64
	rep.Applications = []AppAnalysis{}
	for _, a := range apps {
		tl := hist[a.ID]
		for i := range tl {
			if i > 0 {
				d := days(tl[i].At.Sub(tl[i-1].At))
				tl[i].DaysFromPrev = &d
				// "Shortlisted" is inferred from the first assessment or interview email
				// itself, so its dwell is always ~0 days; leave it out of the chart.
				if tl[i-1].Stage != "shortlisted" {
					k, l := dwellKey(tl[i-1])
					dwell[k] = append(dwell[k], tl[i].At.Sub(tl[i-1].At).Hours()/24)
					dwellLabel[k] = l
				}
			}
		}
		aa := AppAnalysis{Application: a, Timeline: tl, Open: a.Stage != "rejected", Fit: fits[a.ID], PostingURL: urls[a.ID]}
		if aa.Timeline == nil {
			aa.Timeline = []TimelineStep{}
		}
		aa.StageSince = a.LastActivityAt
		if n := len(tl); n > 0 {
			aa.StageSince = tl[n-1].At
			applied := tl[0].At
			rep.Funnel.Applied++
			responded := false
			for _, st := range tl {
				switch st.Stage {
				case "shortlisted":
					rep.Funnel.Shortlisted++
					if !responded {
						responded = true
						toResponse = append(toResponse, st.At.Sub(applied).Hours()/24)
					}
				case "interview":
					aa.InterviewCount = st.Round
					if st.Round == 1 {
						rep.Funnel.Interviewed++
					}
				case "offer", "rejected":
					d := days(st.At.Sub(applied))
					aa.Outcome, aa.DaysToOutcome = st.Stage, &d
					if st.Stage == "offer" {
						rep.Funnel.Offers++
						toOffer = append(toOffer, st.At.Sub(applied).Hours()/24)
					} else {
						rep.Funnel.Rejected++
						toReject = append(toReject, st.At.Sub(applied).Hours()/24)
					}
					if !responded { // an outcome with no earlier step is the first response
						responded = true
						toResponse = append(toResponse, st.At.Sub(applied).Hours()/24)
					}
				}
			}
			if responded {
				rep.Funnel.Responded++
			}
		}
		aa.DaysInStage = days(now.Sub(aa.StageSince))
		rep.Applications = append(rep.Applications, aa)
	}
	if rep.Funnel.Applied > 0 {
		rep.Funnel.ResponseRate = math.Round(float64(rep.Funnel.Responded)/float64(rep.Funnel.Applied)*1000) / 10
	}

	order := []string{"applied", "assessment", "interview_1", "interview_2", "interview_3plus", "offer"}
	rep.StageDwell = []DurationStat{}
	for _, k := range order {
		if xs, ok := dwell[k]; ok {
			rep.StageDwell = append(rep.StageDwell, stat(k, dwellLabel[k], xs))
		}
	}
	rep.Outcomes = []DurationStat{
		stat("first_response", "Applied → first response", toResponse),
		stat("rejection", "Applied → rejection", toReject),
		stat("offer", "Applied → offer", toOffer),
	}

	// Fast automated rejections pull the median down; never nudge before 7 days.
	typicalResponse := 10.0
	if rep.Outcomes[0].N >= 3 {
		typicalResponse = math.Max(7, rep.Outcomes[0].MedianDays)
	}
	for i := range rep.Applications {
		rep.Applications[i].NextMove = nextMove(rep.Applications[i], typicalResponse)
	}
	return rep, nil
}

// nextMove suggests one concrete action from the stage and how long it has sat there.
func nextMove(a AppAnalysis, typicalResponse float64) NextMove {
	d := a.DaysInStage
	switch a.Stage {
	case "applied":
		switch {
		case float64(d) < typicalResponse:
			return NextMove{"Wait", fmt.Sprintf("Companies usually reply to you within about %.0f days. Use the time to research the team.", typicalResponse), false}
		case d < 21:
			return NextMove{"Send a follow-up", "No reply yet. Send a short note to the recruiter or a team member, restating your fit in two lines.", true}
		case d < 45:
			return NextMove{"Find a referral", "Gone quiet for 3+ weeks. A referral or a note to the hiring manager is the best way to revive it.", true}
		default:
			return NextMove{"Consider it closed", "No response in over 6 weeks. Mark it closed and focus on active roles.", false}
		}
	case "assessment":
		if d <= 7 {
			return NextMove{"Complete the assessment", "Assessments usually expire in 5–7 days. Finish it and confirm submission.", true}
		}
		return NextMove{"Chase the result", "It's been over a week since the assessment. Ask the recruiter for the result and next steps.", true}
	case "interview":
		switch {
		case d <= 2:
			return NextMove{"Send thank-you notes", "Thank each interviewer within 48 hours, with one point you'd like to add.", true}
		case d <= 10:
			return NextMove{"Prepare the next round", "Feedback typically arrives in 1–2 weeks. Prepare for the next round now.", false}
		default:
			return NextMove{"Ask for feedback", fmt.Sprintf("%d days since the last interview. Ask the recruiter for the outcome and timeline.", d), true}
		}
	case "offer":
		return NextMove{"Decide on the offer", "Compare fixed and variable pay, level and team. Negotiate before the response deadline.", true}
	case "rejected":
		return NextMove{"Ask for feedback", "Ask briefly for feedback and note what to change for similar roles.", false}
	}
	return NextMove{"Review", "Check this application's latest email.", false}
}

func (s *Store) histories(ctx context.Context, userID string) (map[int64][]TimelineStep, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT application_id, stage, round, entered_at, source FROM stage_history
		WHERE user_id = ? ORDER BY application_id, entered_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]TimelineStep{}
	for rows.Next() {
		var id int64
		var st TimelineStep
		var at string
		if err := rows.Scan(&id, &st.Stage, &st.Round, &at, &st.Source); err != nil {
			return nil, err
		}
		st.At, _ = time.Parse(time.RFC3339, at)
		st.Label = label(st.Stage, st.Round)
		out[id] = append(out[id], st)
	}
	return out, rows.Err()
}

func (s *Store) latestFits(ctx context.Context, userID string) (map[int64]*FitSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.application_id, f.total, f.verdict, f.created_at FROM fit_scores f
		WHERE f.user_id = ? AND f.id = (SELECT id FROM fit_scores g WHERE g.application_id = f.application_id ORDER BY created_at DESC, id DESC LIMIT 1)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]*FitSummary{}
	for rows.Next() {
		var id int64
		f := &FitSummary{}
		var at string
		if err := rows.Scan(&id, &f.Total, &f.Verdict, &at); err != nil {
			return nil, err
		}
		f.CreatedAt, _ = time.Parse(time.RFC3339, at)
		out[id] = f
	}
	return out, rows.Err()
}

func (s *Store) postingURLs(ctx context.Context, userID string) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, COALESCE(NULLIF(p.url, ''), (SELECT job_url FROM events e WHERE e.application_id = a.id AND job_url != '' ORDER BY occurred_at DESC LIMIT 1), '')
		FROM applications a LEFT JOIN job_postings p ON p.application_id = a.id WHERE a.user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var u string
		if err := rows.Scan(&id, &u); err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, rows.Err()
}

// ---------- Detail: posting, fit breakdown, contacts, drafts ----------

type Posting struct {
	Status           string    `json:"status"`
	Source           string    `json:"source"`
	URL              string    `json:"url"`
	Title            string    `json:"title"`
	Location         string    `json:"location"`
	WorkMode         string    `json:"work_mode"`
	Seniority        string    `json:"seniority"`
	MinYears         *int      `json:"min_years"`
	ProductContext   string    `json:"product_context"`
	HiringManager    string    `json:"hiring_manager_name"`
	HiringManagerTtl string    `json:"hiring_manager_title"`
	Requirements     []string  `json:"requirements"`
	Qualifications   []string  `json:"qualifications"`
	Responsibilities []string  `json:"responsibilities"`
	FetchedAt        time.Time `json:"fetched_at"`
}

type FitFactor struct {
	Factor    string `json:"factor"`
	Score     int    `json:"score"`
	Rationale string `json:"rationale"`
}

type Evidence struct {
	Requirement string `json:"requirement"`
	Evidence    string `json:"evidence"`
	Strength    string `json:"strength"`
}

type Fit struct {
	Total       int         `json:"total"`
	Verdict     string      `json:"verdict"`
	Model       string      `json:"model"`
	Pipeline    string      `json:"pipeline"`
	CreatedAt   time.Time   `json:"created_at"`
	Factors     []FitFactor `json:"factors"`
	Suggestions []string    `json:"suggestions"`
	Strengths   []string    `json:"strengths"`
	Gaps        []string    `json:"gaps"`
	Evidence    []Evidence  `json:"evidence"`
}

type Contact struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Email      string    `json:"email"`
	RoleHint   string    `json:"role_hint"`
	Source     string    `json:"source"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type OutreachDraft struct {
	ID              int64     `json:"id"`
	ContactID       *int64    `json:"contact_id"`
	Subject         string    `json:"subject"`
	Body            string    `json:"body"`
	ProductAnalysis string    `json:"product_analysis"`
	Experience      []string  `json:"experience_points"`
	ValueAdds       []string  `json:"value_adds"`
	Improvements    []string  `json:"improvement_ideas"`
	Model           string    `json:"model"`
	CreatedAt       time.Time `json:"created_at"`
}

type AppDetail struct {
	Posting  *Posting        `json:"posting"`
	Fit      *Fit            `json:"fit"`
	Contacts []Contact       `json:"contacts"`
	Drafts   []OutreachDraft `json:"drafts"`
	JobLinks []string        `json:"job_links"` // links found in the application's emails
	// When the user queued a posting search for Claude in the app (nil when not queued).
	ResearchRequestedAt *time.Time `json:"research_requested_at"`
}

var ErrBadContact = errors.New("contact does not belong to this application")

var FitFactors = []string{"experience", "location", "seniority", "role_alignment", "interest_intent"}

func (s *Store) Detail(ctx context.Context, userID string, appID int64) (AppDetail, error) {
	d := AppDetail{Contacts: []Contact{}, Drafts: []OutreachDraft{}, JobLinks: []string{}}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM applications WHERE user_id = ? AND id = ?`, userID, appID).Scan(&exists); err != nil {
		return d, err
	}
	if exists == 0 {
		return d, ErrNotFound
	}
	var err error
	if d.ResearchRequestedAt, err = s.researchRequestedAt(ctx, userID, "job_posting", appID); err != nil {
		return d, err
	}

	var p Posting
	var pid int64
	var minYears sql.NullInt64
	var fetched string
	err = s.db.QueryRowContext(ctx, `SELECT id, status, source, url, title, location, work_mode, seniority, min_years, product_context,
		hiring_manager_name, hiring_manager_title, fetched_at
		FROM job_postings WHERE user_id = ? AND application_id = ?`, userID, appID).
		Scan(&pid, &p.Status, &p.Source, &p.URL, &p.Title, &p.Location, &p.WorkMode, &p.Seniority, &minYears, &p.ProductContext,
			&p.HiringManager, &p.HiringManagerTtl, &fetched)
	if err == nil {
		if minYears.Valid {
			v := int(minYears.Int64)
			p.MinYears = &v
		}
		p.FetchedAt, _ = time.Parse(time.RFC3339, fetched)
		p.Requirements, p.Qualifications, p.Responsibilities = []string{}, []string{}, []string{}
		rows, err := s.db.QueryContext(ctx, `SELECT kind, text FROM job_posting_items WHERE posting_id = ? ORDER BY kind, position`, pid)
		if err != nil {
			return d, err
		}
		for rows.Next() {
			var kind, text string
			if err := rows.Scan(&kind, &text); err != nil {
				rows.Close()
				return d, err
			}
			switch kind {
			case "requirement":
				p.Requirements = append(p.Requirements, text)
			case "qualification":
				p.Qualifications = append(p.Qualifications, text)
			case "responsibility":
				p.Responsibilities = append(p.Responsibilities, text)
			}
		}
		rows.Close()
		d.Posting = &p
	} else if !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}

	var f Fit
	var fid int64
	var created string
	err = s.db.QueryRowContext(ctx, `SELECT id, total, verdict, model, pipeline, created_at FROM fit_scores
		WHERE user_id = ? AND application_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, userID, appID).
		Scan(&fid, &f.Total, &f.Verdict, &f.Model, &f.Pipeline, &created)
	if err == nil {
		f.CreatedAt, _ = time.Parse(time.RFC3339, created)
		f.Factors, f.Suggestions = []FitFactor{}, []string{}
		rows, err := s.db.QueryContext(ctx, `SELECT factor, score, rationale FROM fit_factors WHERE score_id = ?
			ORDER BY CASE factor WHEN 'experience' THEN 1 WHEN 'location' THEN 2 WHEN 'seniority' THEN 3 WHEN 'role_alignment' THEN 4 ELSE 5 END`, fid)
		if err != nil {
			return d, err
		}
		for rows.Next() {
			var ff FitFactor
			if err := rows.Scan(&ff.Factor, &ff.Score, &ff.Rationale); err != nil {
				rows.Close()
				return d, err
			}
			f.Factors = append(f.Factors, ff)
		}
		rows.Close()
		if f.Suggestions, err = s.texts(ctx, `SELECT text FROM fit_suggestions WHERE score_id = ? ORDER BY position`, fid); err != nil {
			return d, err
		}
		if f.Strengths, err = s.texts(ctx, `SELECT text FROM fit_points WHERE score_id = ? AND kind = 'strength' ORDER BY position`, fid); err != nil {
			return d, err
		}
		if f.Gaps, err = s.texts(ctx, `SELECT text FROM fit_points WHERE score_id = ? AND kind = 'gap' ORDER BY position`, fid); err != nil {
			return d, err
		}
		f.Evidence = []Evidence{}
		erows, err := s.db.QueryContext(ctx, `SELECT requirement, evidence, strength FROM fit_evidence WHERE score_id = ? ORDER BY position`, fid)
		if err != nil {
			return d, err
		}
		for erows.Next() {
			var e Evidence
			if err := erows.Scan(&e.Requirement, &e.Evidence, &e.Strength); err != nil {
				erows.Close()
				return d, err
			}
			f.Evidence = append(f.Evidence, e)
		}
		erows.Close()
		d.Fit = &f
	} else if !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, name, email, role_hint, source, last_seen_at FROM contacts
		WHERE user_id = ? AND application_id = ? ORDER BY last_seen_at DESC`, userID, appID)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var c Contact
		var at string
		if err := rows.Scan(&c.ID, &c.Name, &c.Email, &c.RoleHint, &c.Source, &at); err != nil {
			rows.Close()
			return d, err
		}
		c.LastSeenAt, _ = time.Parse(time.RFC3339, at)
		d.Contacts = append(d.Contacts, c)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, `SELECT id, contact_id, subject, body, product_analysis, model, created_at FROM outreach_drafts
		WHERE user_id = ? AND application_id = ? ORDER BY created_at DESC, id DESC LIMIT 5`, userID, appID)
	if err != nil {
		return d, err
	}
	for rows.Next() {
		var o OutreachDraft
		var cid sql.NullInt64
		var at string
		if err := rows.Scan(&o.ID, &cid, &o.Subject, &o.Body, &o.ProductAnalysis, &o.Model, &at); err != nil {
			rows.Close()
			return d, err
		}
		if cid.Valid {
			o.ContactID = &cid.Int64
		}
		o.CreatedAt, _ = time.Parse(time.RFC3339, at)
		d.Drafts = append(d.Drafts, o)
	}
	rows.Close()
	for i := range d.Drafts {
		o := &d.Drafts[i]
		q := `SELECT text FROM outreach_points WHERE draft_id = ? AND kind = ? ORDER BY position`
		if o.Experience, err = s.texts(ctx, q, o.ID, "experience"); err != nil {
			return d, err
		}
		if o.ValueAdds, err = s.texts(ctx, q, o.ID, "value_add"); err != nil {
			return d, err
		}
		if o.Improvements, err = s.texts(ctx, q, o.ID, "improvement"); err != nil {
			return d, err
		}
	}
	// The user's own link first, then links found in the emails.
	d.JobLinks, err = s.texts(ctx, `SELECT job_url FROM applications WHERE user_id = ? AND id = ? AND job_url != ''
		UNION ALL SELECT DISTINCT e.job_url FROM events e WHERE e.user_id = ? AND e.application_id = ? AND e.job_url != ''
		  AND e.job_url != (SELECT job_url FROM applications WHERE id = ?)`, userID, appID, userID, appID, appID)
	return d, err
}

func (s *Store) texts(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---------- Writes from the agent service ----------

func (p Posting) Validate() error {
	if p.Status != "found" && p.Status != "not_found" {
		return errors.New("status must be found or not_found")
	}
	if p.Source != "email_link" && p.Source != "web_search" && p.Source != "none" {
		return errors.New("invalid source")
	}
	if p.URL != "" && len(p.URL) > 2000 {
		return errors.New("url too long")
	}
	if len(p.Requirements)+len(p.Qualifications)+len(p.Responsibilities) > 120 {
		return errors.New("too many items")
	}
	return nil
}

func (s *Store) SavePosting(ctx context.Context, userID string, appID int64, p Posting, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownsApp(ctx, tx, userID, appID); err != nil {
		return err
	}
	if p.Status == "found" {
		if err := markResearchDone(ctx, tx, userID, "job_posting", appID, now); err != nil {
			return err
		}
	}
	var minYears any
	if p.MinYears != nil {
		minYears = *p.MinYears
	}
	var pid int64
	err = tx.QueryRowContext(ctx, `INSERT INTO job_postings
		(user_id, application_id, status, source, url, title, location, work_mode, seniority, min_years, product_context,
		 hiring_manager_name, hiring_manager_title, fetched_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (application_id) DO UPDATE SET status = excluded.status, source = excluded.source, url = excluded.url,
			title = excluded.title, location = excluded.location, work_mode = excluded.work_mode, seniority = excluded.seniority,
			min_years = excluded.min_years, product_context = excluded.product_context,
			hiring_manager_name = excluded.hiring_manager_name, hiring_manager_title = excluded.hiring_manager_title,
			fetched_at = excluded.fetched_at
		RETURNING id`,
		userID, appID, p.Status, p.Source, p.URL, truncate(p.Title, 200), truncate(p.Location, 200), p.WorkMode, truncate(p.Seniority, 100),
		minYears, truncate(p.ProductContext, 3000), truncate(p.HiringManager, 120), truncate(p.HiringManagerTtl, 160),
		now.UTC().Format(time.RFC3339)).Scan(&pid)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM job_posting_items WHERE posting_id = ?`, pid); err != nil {
		return err
	}
	for kind, items := range map[string][]string{"requirement": p.Requirements, "qualification": p.Qualifications, "responsibility": p.Responsibilities} {
		for i, t := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO job_posting_items (posting_id, kind, position, text) VALUES (?, ?, ?, ?)`,
				pid, kind, i, truncate(t, 500)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (f Fit) Validate() error {
	if len(f.Factors) != len(FitFactors) {
		return fmt.Errorf("need exactly %d factors", len(FitFactors))
	}
	seen := map[string]bool{}
	sum := 0
	for _, ff := range f.Factors {
		ok := false
		for _, name := range FitFactors {
			ok = ok || ff.Factor == name
		}
		if !ok || seen[ff.Factor] {
			return fmt.Errorf("invalid or duplicate factor %q", ff.Factor)
		}
		if ff.Score < 0 || ff.Score > 20 {
			return fmt.Errorf("factor %s score out of range", ff.Factor)
		}
		seen[ff.Factor] = true
		sum += ff.Score
	}
	if f.Total != sum {
		return fmt.Errorf("total %d must equal the sum of factors %d", f.Total, sum)
	}
	if f.Model == "" {
		return errors.New("model is required")
	}
	if len(f.Evidence) > 80 || len(f.Strengths) > 10 || len(f.Gaps) > 10 || len(f.Suggestions) > 10 {
		return errors.New("too many evidence items or points")
	}
	for _, e := range f.Evidence {
		if e.Strength != "strong" && e.Strength != "partial" && e.Strength != "missing" {
			return fmt.Errorf("invalid evidence strength %q", e.Strength)
		}
	}
	return nil
}

func (s *Store) SaveFit(ctx context.Context, userID string, appID int64, f Fit, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := ownsApp(ctx, tx, userID, appID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO fit_scores (user_id, application_id, total, verdict, model, pipeline, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, appID, f.Total, truncate(f.Verdict, 500), truncate(f.Model, 100), truncate(f.Pipeline, 400), now.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	sid, _ := res.LastInsertId()
	for _, ff := range f.Factors {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fit_factors (score_id, factor, score, rationale) VALUES (?, ?, ?, ?)`,
			sid, ff.Factor, ff.Score, truncate(ff.Rationale, 1500)); err != nil {
			return err
		}
	}
	for i, t := range f.Suggestions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fit_suggestions (score_id, position, text) VALUES (?, ?, ?)`, sid, i, truncate(t, 500)); err != nil {
			return err
		}
	}
	for kind, items := range map[string][]string{"strength": f.Strengths, "gap": f.Gaps} {
		for i, t := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO fit_points (score_id, kind, position, text) VALUES (?, ?, ?, ?)`, sid, kind, i, truncate(t, 500)); err != nil {
				return err
			}
		}
	}
	for i, e := range f.Evidence {
		if _, err := tx.ExecContext(ctx, `INSERT INTO fit_evidence (score_id, position, requirement, evidence, strength) VALUES (?, ?, ?, ?, ?)`,
			sid, i, truncate(e.Requirement, 500), truncate(e.Evidence, 800), e.Strength); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SaveOutreach(ctx context.Context, userID string, appID int64, o OutreachDraft, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err := ownsApp(ctx, tx, userID, appID); err != nil {
		return 0, err
	}
	var cid any
	if o.ContactID != nil {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts WHERE id = ? AND user_id = ? AND application_id = ?`,
			*o.ContactID, userID, appID).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, ErrBadContact
		}
		cid = *o.ContactID
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO outreach_drafts (user_id, application_id, contact_id, subject, body, product_analysis, model, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, userID, appID, cid, truncate(o.Subject, 300), truncate(o.Body, 8000),
		truncate(o.ProductAnalysis, 4000), o.Model, now.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	did, _ := res.LastInsertId()
	for kind, items := range map[string][]string{"experience": o.Experience, "value_add": o.ValueAdds, "improvement": o.Improvements} {
		for i, t := range items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO outreach_points (draft_id, kind, position, text) VALUES (?, ?, ?, ?)`,
				did, kind, i, truncate(t, 800)); err != nil {
				return 0, err
			}
		}
	}
	return did, tx.Commit()
}

type LLMUsage struct {
	ApplicationID *int64 `json:"application_id"`
	Purpose       string `json:"purpose"`
	Model         string `json:"model"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	WebSearches   int64  `json:"web_searches"`
}

func (s *Store) RecordUsage(ctx context.Context, userID string, u LLMUsage, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO llm_usage (user_id, application_id, purpose, model, input_tokens, output_tokens, web_searches, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, userID, u.ApplicationID, truncate(u.Purpose, 50), truncate(u.Model, 100),
		u.InputTokens, u.OutputTokens, u.WebSearches, now.UTC().Format(time.RFC3339))
	return err
}

func ownsApp(ctx context.Context, tx *sql.Tx, userID string, appID int64) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM applications WHERE user_id = ? AND id = ?`, userID, appID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
