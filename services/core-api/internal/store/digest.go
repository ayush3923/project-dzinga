package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
)

// The morning digest: what the last refresh brought in, computed with plain
// code. A short text version is written by a template, or once per refresh by
// a small Sonnet 5 call (the agent saves it with SaveDigestText).

// IST: the user's day boundaries for the 7-day series (fixed offset; no tzdata needed).
var IST = time.FixedZone("IST", 5*3600+1800)

// arrived is when an email reached Dzinga; emails pulled in before v8 count as
// arriving when they were sent.
const arrived = `COALESCE(NULLIF(e.ingested_at, ''), e.occurred_at)`

type DigestApp struct {
	ID      int64  `json:"id"`
	Company string `json:"company"`
	Role    string `json:"role"`
	Kind    string `json:"kind,omitempty"` // responses: interview | assessment | offer | rejected | update
}

type DigestStep struct {
	ID      int64  `json:"id"`
	Company string `json:"company"`
	Role    string `json:"role"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
}

type DigestTrends struct {
	AppsThisWeek       int     `json:"apps_this_week"`
	AppsLastWeek       int     `json:"apps_last_week"`
	ResponsesThisWeek  int     `json:"responses_this_week"`
	RejectionsThisWeek int     `json:"rejections_this_week"`
	ResponseRate30d    float64 `json:"response_rate_30d"` // share of last-30-day applications with any reply
	Waiting            int     `json:"waiting"`
}

type DayPoint struct {
	Date         string `json:"date"` // YYYY-MM-DD (IST)
	Applications int    `json:"applications"`
	Rejections   int    `json:"rejections"`
	Waiting      int    `json:"waiting"` // no reply yet, applied within the previous 45 days
}

type Digest struct {
	RefreshID       int64        `json:"refresh_id"` // 0 when no refresh is recorded yet
	RefreshAt       *time.Time   `json:"refresh_at"`
	Since           time.Time    `json:"since"` // the window's start
	NewApplications []DigestApp  `json:"new_applications"`
	Responses       []DigestApp  `json:"responses"`
	Trends          DigestTrends `json:"trends"`
	NextSteps       []DigestStep `json:"next_steps"`
	Series          []DayPoint   `json:"series"`
	Text            string       `json:"text"`
	Model           string       `json:"model"` // template | claude-sonnet-5 | …
}

// waitingWindow: an application with no reply after this long is treated as closed.
const waitingWindow = 45 * 24 * time.Hour

// RecordRefresh logs a finished refresh and how many emails it added.
func (s *Store) RecordRefresh(ctx context.Context, userID string, now time.Time) (int64, error) {
	var prev string
	err := s.db.QueryRowContext(ctx, `SELECT at FROM refreshes WHERE user_id = ? ORDER BY at DESC, id DESC LIMIT 1`, userID).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var added int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE user_id = ? AND ingested_at > ?`, userID, prev).Scan(&added); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO refreshes (user_id, at, added) VALUES (?, ?, ?)`, userID, now.UTC().Format(time.RFC3339Nano), added)
	if err != nil {
		return 0, err
	}
	if err := s.queueAfterRefresh(ctx, userID, prev, now); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveDigestText stores the written digest for a refresh. Only the latest refresh
// is accepted, so a slow writer can't overwrite a newer digest with an old one.
func (s *Store) SaveDigestText(ctx context.Context, userID string, refreshID int64, text, model string, now time.Time) error {
	var latest int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM refreshes WHERE user_id = ?`, userID).Scan(&latest); err != nil {
		return err
	}
	if refreshID == 0 || refreshID != latest {
		return ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO digests (user_id, refresh_id, text, model, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, refresh_id) DO UPDATE SET text = excluded.text, model = excluded.model, created_at = excluded.created_at`,
		userID, refreshID, truncate(strings.TrimSpace(text), 1200), truncate(model, 64), now.UTC().Format(time.RFC3339))
	return err
}

// Digest computes the morning digest for the window since the previous refresh.
func (s *Store) Digest(ctx context.Context, userID string, now time.Time) (Digest, error) {
	d := Digest{NewApplications: []DigestApp{}, Responses: []DigestApp{}, NextSteps: []DigestStep{}}

	// The window: what the latest refresh brought in (since the one before it).
	var at []string
	var ids []int64
	if err := s.scanRows(ctx, `SELECT id, at FROM refreshes WHERE user_id = ? ORDER BY at DESC, id DESC LIMIT 2`, userID, func(r *sql.Rows) error {
		var id int64
		var t string
		err := r.Scan(&id, &t)
		ids, at = append(ids, id), append(at, t)
		return err
	}); err != nil {
		return d, err
	}
	since := now.Add(-24 * time.Hour)
	until := now
	if len(at) > 0 {
		d.RefreshID = ids[0]
		t, _ := time.Parse(time.RFC3339Nano, at[0])
		d.RefreshAt, until = &t, t.Add(time.Second)
		since = t.Add(-24 * time.Hour) // a first refresh covers the day before it
		if len(at) > 1 {
			since, _ = time.Parse(time.RFC3339Nano, at[1])
		}
	}
	d.Since = since
	sinceS, untilS := since.UTC().Format(time.RFC3339Nano), until.UTC().Format(time.RFC3339Nano)

	// New applications: first email arrived in the window, and there is an
	// application email. (A lone rejection for an application whose own email
	// isn't in any mailbox is a reply, not something new.)
	if err := s.queryRows(ctx, `SELECT a.id, a.company, a.role FROM applications a
		WHERE a.user_id = ? AND (SELECT MIN(`+arrived+`) FROM events e WHERE e.application_id = a.id) > ?
		  AND (SELECT MIN(`+arrived+`) FROM events e WHERE e.application_id = a.id) <= ?
		  AND EXISTS (SELECT 1 FROM events e WHERE e.application_id = a.id AND e.kind = 'applied')
		ORDER BY a.applied_at DESC`, []any{userID, sinceS, untilS}, func(r *sql.Rows) error {
		var x DigestApp
		err := r.Scan(&x.ID, &x.Company, &x.Role)
		d.NewApplications = append(d.NewApplications, x)
		return err
	}); err != nil {
		return d, err
	}
	// Responses: any reply that arrived in the window (latest per application).
	seen := map[int64]bool{}
	if err := s.queryRows(ctx, `SELECT a.id, a.company, a.role, e.kind FROM events e JOIN applications a ON a.id = e.application_id
		WHERE e.user_id = ? AND e.kind != 'applied' AND `+arrived+` > ? AND `+arrived+` <= ?
		ORDER BY e.occurred_at DESC`, []any{userID, sinceS, untilS}, func(r *sql.Rows) error {
		var x DigestApp
		if err := r.Scan(&x.ID, &x.Company, &x.Role, &x.Kind); err != nil {
			return err
		}
		if !seen[x.ID] {
			seen[x.ID] = true
			d.Responses = append(d.Responses, x)
		}
		return nil
	}); err != nil {
		return d, err
	}

	if err := s.trends(ctx, userID, now, &d); err != nil {
		return d, err
	}
	if err := s.nextSteps(ctx, userID, now, &d); err != nil {
		return d, err
	}

	d.Text, d.Model = DigestTemplate(d), "template"
	if d.RefreshID != 0 {
		var text, model string
		err := s.db.QueryRowContext(ctx, `SELECT text, model FROM digests WHERE user_id = ? AND refresh_id = ?`, userID, d.RefreshID).Scan(&text, &model)
		if err == nil && text != "" {
			d.Text, d.Model = text, model
		} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return d, err
		}
	}
	return d, nil
}

func (s *Store) queryRows(ctx context.Context, query string, args []any, each func(*sql.Rows) error) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
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

type appEvents struct {
	applied   time.Time
	firstResp *time.Time
	rejects   []time.Time
}

func (s *Store) trends(ctx context.Context, userID string, now time.Time, d *Digest) error {
	apps := map[int64]*appEvents{}
	if err := s.scanRows(ctx, `SELECT a.id, a.applied_at, e.kind, e.occurred_at FROM applications a JOIN events e ON e.application_id = a.id
		WHERE a.user_id = ? ORDER BY e.occurred_at`, userID, func(r *sql.Rows) error {
		var id int64
		var appliedS, kind, atS string
		if err := r.Scan(&id, &appliedS, &kind, &atS); err != nil {
			return err
		}
		a := apps[id]
		if a == nil {
			t, _ := time.Parse(time.RFC3339, appliedS)
			a = &appEvents{applied: t}
			apps[id] = a
		}
		t, _ := time.Parse(time.RFC3339, atS)
		if kind != "applied" && a.firstResp == nil {
			a.firstResp = &t
		}
		if kind == "rejected" {
			a.rejects = append(a.rejects, t)
		}
		return nil
	}); err != nil {
		return err
	}

	// Day boundaries in IST, oldest first; today is the last point.
	today := time.Date(now.In(IST).Year(), now.In(IST).Month(), now.In(IST).Day(), 0, 0, 0, 0, IST)
	weekStart, lastWeekStart := today.AddDate(0, 0, -6), today.AddDate(0, 0, -13)
	d.Series = make([]DayPoint, 7)
	for i := range d.Series {
		d.Series[i].Date = weekStart.AddDate(0, 0, i).Format("2006-01-02")
	}
	waitingAt := func(end time.Time) int {
		n := 0
		for _, a := range apps {
			if !a.applied.After(end) && end.Sub(a.applied) <= waitingWindow && (a.firstResp == nil || a.firstResp.After(end)) {
				n++
			}
		}
		return n
	}
	dayOf := func(t time.Time) int { return int(t.In(IST).Sub(weekStart).Hours() / 24) }
	var recent30, replied30 int
	for _, a := range apps {
		if i := dayOf(a.applied); !a.applied.Before(weekStart) && i < 7 {
			d.Series[i].Applications++
			d.Trends.AppsThisWeek++
		} else if !a.applied.Before(lastWeekStart) && a.applied.Before(weekStart) {
			d.Trends.AppsLastWeek++
		}
		for _, r := range a.rejects {
			if i := dayOf(r); !r.Before(weekStart) && i < 7 {
				d.Series[i].Rejections++
				d.Trends.RejectionsThisWeek++
			}
		}
		if a.firstResp != nil && !a.firstResp.Before(weekStart) && !a.firstResp.After(now) {
			d.Trends.ResponsesThisWeek++
		}
		if now.Sub(a.applied) <= 30*24*time.Hour {
			recent30++
			if a.firstResp != nil {
				replied30++
			}
		}
	}
	for i := range d.Series {
		end := weekStart.AddDate(0, 0, i+1).Add(-time.Nanosecond)
		if end.After(now) {
			end = now
		}
		d.Series[i].Waiting = waitingAt(end)
	}
	d.Trends.Waiting = waitingAt(now)
	if recent30 > 0 {
		d.Trends.ResponseRate30d = float64(replied30) / float64(recent30)
	}
	return nil
}

// stagePriority: live conversations first, then follow-ups.
var stagePriority = map[string]int{"offer": 0, "interview": 1, "assessment": 2, "shortlisted": 3, "applied": 4}

func (s *Store) nextSteps(ctx context.Context, userID string, now time.Time, d *Digest) error {
	rep, err := s.Analysis(ctx, userID, now)
	if err != nil {
		return err
	}
	var urgent []AppAnalysis
	for _, a := range rep.Applications {
		if a.Open && a.NextMove.Urgent {
			urgent = append(urgent, a)
		}
	}
	sort.SliceStable(urgent, func(i, j int) bool {
		pi, pj := stagePriority[urgent[i].Stage], stagePriority[urgent[j].Stage]
		if pi != pj {
			return pi < pj
		}
		return urgent[i].DaysInStage < urgent[j].DaysInStage // the freshest follow-up is the most likely to land
	})
	for _, a := range urgent {
		if len(d.NextSteps) == 3 {
			break
		}
		d.NextSteps = append(d.NextSteps, DigestStep{ID: a.ID, Company: a.Company, Role: a.Role, Title: a.NextMove.Title, Detail: a.NextMove.Detail})
	}
	return nil
}

// DigestTemplate writes the digest in at most 100 words without a model.
func DigestTemplate(d Digest) string {
	var parts []string
	names := func(list []DigestApp, n int) string {
		var out []string
		for i, a := range list {
			if i == n {
				out = append(out, fmt.Sprintf("%d more", len(list)-n))
				break
			}
			out = append(out, a.Company)
		}
		return strings.Join(out, ", ")
	}
	switch len(d.NewApplications) {
	case 0:
		parts = append(parts, "No new applications since the last refresh.")
	default:
		parts = append(parts, fmt.Sprintf("%d new application%s: %s.", len(d.NewApplications), plural(len(d.NewApplications), "", "s"), names(d.NewApplications, 4)))
	}
	if len(d.Responses) == 0 {
		parts = append(parts, "No replies came in.")
	} else {
		// Only a step forward is good news; a plain status update is not.
		var good, updates, rej []DigestApp
		for _, r := range d.Responses {
			switch r.Kind {
			case "rejected":
				rej = append(rej, r)
			case "update":
				updates = append(updates, r)
			default: // assessment, interview, offer
				good = append(good, r)
			}
		}
		if len(good) > 0 {
			parts = append(parts, fmt.Sprintf("Good news from %s.", names(good, 3)))
		}
		if len(updates) > 0 {
			parts = append(parts, fmt.Sprintf("Status update%s from %s.", plural(len(updates), "", "s"), names(updates, 3)))
		}
		if len(rej) > 0 {
			parts = append(parts, fmt.Sprintf("Rejections from %s.", names(rej, 3)))
		}
	}
	t := d.Trends
	trend := "steady"
	switch {
	case t.AppsThisWeek > t.AppsLastWeek:
		trend = "up"
	case t.AppsThisWeek < t.AppsLastWeek:
		trend = "down"
	}
	parts = append(parts, fmt.Sprintf("This week: %d applications (%s from %d), %d replies, %d waiting; %.0f%% of last month's applications got a reply.",
		t.AppsThisWeek, trend, t.AppsLastWeek, t.ResponsesThisWeek, t.Waiting, t.ResponseRate30d*100))
	if len(d.NextSteps) > 0 {
		var steps []string
		for i, s := range d.NextSteps {
			if i == 2 {
				break
			}
			steps = append(steps, fmt.Sprintf("%s for %s", lowerFirst(s.Title), s.Company))
		}
		parts = append(parts, "Next: "+strings.Join(steps, "; ")+".")
	}
	return limitWords(strings.Join(parts, " "), 100)
}

func limitWords(s string, n int) string {
	w := strings.Fields(s)
	if len(w) <= n {
		return s
	}
	return strings.TrimRight(strings.Join(w[:n], " "), ",;:") + "…"
}

// lowerFirst lowercases a title's first letter to continue a sentence ("Round 2
// (TP2) interview, Fri 20 Nov" → "round 2 (TP2) interview, Fri 20 Nov"). Names,
// days, months and acronyms keep their capitals; so does a leading acronym.
func lowerFirst(s string) string {
	r := []rune(s)
	if len(r) == 0 || (len(r) > 1 && unicode.IsUpper(r[1])) {
		return s
	}
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
