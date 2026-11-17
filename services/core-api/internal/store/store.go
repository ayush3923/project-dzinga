// Package store owns core-api's data: applications, their email events, and the
// user's intent. Every query is scoped by user ID so multi-user support needs no
// schema change.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite"
)

// Stages, in pipeline order. "update" events never move an application's stage.
var Stages = []string{"applied", "assessment", "interview", "offer", "rejected"}

func ValidStage(s string) bool {
	for _, v := range Stages {
		if v == s {
			return true
		}
	}
	return false
}

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS applications (
	id               INTEGER PRIMARY KEY,
	user_id          TEXT NOT NULL,
	key              TEXT NOT NULL,
	company          TEXT NOT NULL,
	role             TEXT NOT NULL DEFAULT '',
	stage            TEXT NOT NULL,
	stage_override   TEXT,
	source_account   TEXT NOT NULL,
	applied_at       TEXT NOT NULL,
	last_activity_at TEXT NOT NULL,
	UNIQUE (user_id, key)
);
CREATE INDEX IF NOT EXISTS idx_apps_user_activity ON applications (user_id, last_activity_at DESC);
CREATE INDEX IF NOT EXISTS idx_apps_user_stage    ON applications (user_id, stage);

CREATE TABLE IF NOT EXISTS events (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT NOT NULL,
	application_id INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	message_id     TEXT NOT NULL,
	account        TEXT NOT NULL,
	occurred_at    TEXT NOT NULL,
	sender         TEXT NOT NULL,
	subject        TEXT NOT NULL,
	snippet        TEXT NOT NULL,
	kind           TEXT NOT NULL,
	UNIQUE (user_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_events_app ON events (application_id, occurred_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5 (
	company, role, subject, snippet, sender,
	application_id UNINDEXED, user_id UNINDEXED,
	tokenize = 'unicode61 remove_diacritics 2'
);

CREATE TABLE IF NOT EXISTS intents (
	user_id    TEXT PRIMARY KEY,
	data       TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps WAL simple for the prototype.
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// IngestEvent is one classified job email, as sent by the ingest service.
type IngestEvent struct {
	MessageID  string    `json:"message_id"`
	Account    string    `json:"account"`
	OccurredAt time.Time `json:"occurred_at"`
	Sender     string    `json:"sender"`
	SenderName string    `json:"sender_name,omitempty"`
	Subject    string    `json:"subject"`
	Snippet    string    `json:"snippet"`
	Company    string    `json:"company"`
	Role       string    `json:"role"`
	Kind       string    `json:"kind"`
	JobURL     string    `json:"job_url,omitempty"` // link to the posting found in the email, if any
}

func (e IngestEvent) Validate() error {
	switch {
	case e.MessageID == "", e.Company == "", e.Account == "":
		return errors.New("message_id, account and company are required")
	case e.OccurredAt.IsZero():
		return errors.New("occurred_at is required")
	case e.Kind != "update" && !ValidStage(e.Kind):
		return fmt.Errorf("invalid kind %q", e.Kind)
	case e.JobURL != "" && !strings.HasPrefix(e.JobURL, "https://"):
		return errors.New("job_url must be https")
	case len(e.Snippet) > 2000, len(e.Subject) > 500, len(e.Company) > 200, len(e.Role) > 200,
		len(e.SenderName) > 200, len(e.JobURL) > 2000:
		return errors.New("field too long")
	}
	return nil
}

// AppKey groups emails about the same application: company plus role when known.
func AppKey(company, role string) string {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return norm(company) + "|" + norm(role)
}

// Ingest stores events idempotently (re-ingesting the same message is a no-op) and
// recomputes each touched application's stage from its latest stage-bearing event.
// Returns how many new events were stored.
func (s *Store) Ingest(ctx context.Context, userID string, events []IngestEvent) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	// Oldest first, whatever order the mailbox returned: a rejection must find the
	// application it answers.
	events = append([]IngestEvent(nil), events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].OccurredAt.Before(events[j].OccurredAt) })
	added := 0
	for _, e := range events {
		var exists int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM events WHERE user_id = ? AND message_id = ?`, userID, e.MessageID,
		).Scan(&exists); err != nil {
			return 0, err
		}
		if exists > 0 {
			// Already stored: only fill in fields that older imports didn't capture.
			var appID int64
			if err := tx.QueryRowContext(ctx, `UPDATE events SET
					job_url     = CASE WHEN job_url = '' THEN ? ELSE job_url END,
					sender_name = CASE WHEN sender_name = '' THEN ? ELSE sender_name END
				WHERE user_id = ? AND message_id = ? RETURNING application_id`,
				e.JobURL, e.SenderName, userID, e.MessageID).Scan(&appID); err != nil {
				return 0, err
			}
			if e.SenderName != "" {
				if _, err := tx.ExecContext(ctx, `DELETE FROM contacts WHERE user_id = ? AND application_id = ? AND email = ? AND name = ''`,
					userID, appID, strings.ToLower(e.Sender)); err != nil {
					return 0, err
				}
			}
			if err := upsertContact(ctx, tx, userID, appID, e); err != nil {
				return 0, err
			}
			continue
		}

		at := e.OccurredAt.UTC().Format(time.RFC3339)
		key, err := chooseKey(ctx, tx, userID, e)
		if err != nil {
			return 0, err
		}
		initial := e.Kind
		if initial == "update" {
			initial = "applied"
		}

		var appID int64
		err = tx.QueryRowContext(ctx, `
			INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (user_id, key) DO UPDATE SET
				last_activity_at = MAX(last_activity_at, excluded.last_activity_at),
				applied_at       = MIN(applied_at, excluded.applied_at),
				role             = CASE WHEN role = '' THEN excluded.role ELSE role END
			RETURNING id`,
			userID, key, e.Company, e.Role, initial, e.Account, at, at,
		).Scan(&appID)
		if err != nil {
			return 0, fmt.Errorf("upsert application: %w", err)
		}

		res, err := tx.ExecContext(ctx, `
			INSERT INTO events (user_id, application_id, message_id, account, occurred_at, sender, sender_name, subject, snippet, kind, job_url, ingested_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, appID, e.MessageID, e.Account, at, e.Sender, e.SenderName, e.Subject, e.Snippet, e.Kind, e.JobURL,
			time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return 0, fmt.Errorf("insert event: %w", err)
		}
		eventID, _ := res.LastInsertId()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO search_fts (rowid, company, role, subject, snippet, sender, application_id, user_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			eventID, e.Company, e.Role, e.Subject, e.Snippet, e.Sender, appID, userID); err != nil {
			return 0, fmt.Errorf("index event: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE applications SET stage = COALESCE((
				SELECT kind FROM events
				WHERE application_id = ? AND kind != 'update'
				ORDER BY occurred_at DESC, id DESC LIMIT 1), stage)
			WHERE id = ?`, appID, appID); err != nil {
			return 0, err
		}
		if err := upsertContact(ctx, tx, userID, appID, e); err != nil {
			return 0, err
		}
		if err := linkOrganization(ctx, tx, userID, appID); err != nil {
			return 0, err
		}
		if err := rebuildHistory(ctx, tx, userID, appID); err != nil {
			return 0, err
		}
		added++
	}
	return added, tx.Commit()
}

type Application struct {
	ID             int64     `json:"id"`
	Company        string    `json:"company"`
	Role           string    `json:"role"`
	Stage          string    `json:"stage"`
	StageIsManual  bool      `json:"stage_is_manual"`
	SourceAccount  string    `json:"source_account"`
	Accounts       []string  `json:"accounts"` // every mailbox this application's emails came from
	AppliedAt      time.Time `json:"applied_at"`
	LastActivityAt time.Time `json:"last_activity_at"`
	EventCount     int       `json:"event_count"`
	OrganizationID int64     `json:"organization_id"`
	JobURL         string    `json:"job_url"`     // added by the user; "" when none
	RoundsDone     int       `json:"rounds_done"` // interview rounds completed, set by the user
	NextStep       string    `json:"next_step"`   // what comes next, set by the user; "" when none
}

type Event struct {
	ID         int64     `json:"id"`
	Account    string    `json:"account"`
	OccurredAt time.Time `json:"occurred_at"`
	Sender     string    `json:"sender"`
	Subject    string    `json:"subject"`
	Snippet    string    `json:"snippet"`
	Kind       string    `json:"kind"`
}

const appColumns = `a.id, a.company, a.role, COALESCE(a.stage_override, a.stage), a.stage_override IS NOT NULL,
	a.source_account, a.applied_at, a.last_activity_at,
	(SELECT COUNT(*) FROM events e WHERE e.application_id = a.id),
	COALESCE((SELECT group_concat(account, char(31)) FROM (SELECT DISTINCT account FROM events e
		WHERE e.application_id = a.id ORDER BY account)), ''), COALESCE(a.organization_id, 0), a.job_url, a.rounds_done, a.next_step`

func scanApp(row interface{ Scan(...any) error }) (Application, error) {
	var a Application
	var applied, last, accounts string
	if err := row.Scan(&a.ID, &a.Company, &a.Role, &a.Stage, &a.StageIsManual,
		&a.SourceAccount, &applied, &last, &a.EventCount, &accounts, &a.OrganizationID, &a.JobURL, &a.RoundsDone, &a.NextStep); err != nil {
		return a, err
	}
	a.Accounts = []string{}
	if accounts != "" {
		a.Accounts = strings.Split(accounts, "\x1f")
	}
	a.AppliedAt, _ = time.Parse(time.RFC3339, applied)
	a.LastActivityAt, _ = time.Parse(time.RFC3339, last)
	return a, nil
}

// ftsQuery turns free text into a safe FTS5 prefix query: every token is quoted, so
// user input can never inject FTS operators or SQL.
func ftsQuery(q string) string {
	var parts []string
	for _, f := range strings.FieldsFunc(q, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len(parts) == 8 {
			break
		}
		parts = append(parts, `"`+f+`"*`)
	}
	return strings.Join(parts, " ")
}

type ListFilter struct {
	Stage string
	Query string
}

func (s *Store) ListApplications(ctx context.Context, userID string, f ListFilter) ([]Application, error) {
	where := []string{"a.user_id = ?"}
	args := []any{userID}
	if f.Stage != "" {
		where = append(where, "COALESCE(a.stage_override, a.stage) = ?")
		args = append(args, f.Stage)
	}
	if q := ftsQuery(f.Query); q != "" {
		where = append(where, "a.id IN (SELECT application_id FROM search_fts WHERE search_fts MATCH ? AND user_id = ?)")
		args = append(args, q, userID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+appColumns+` FROM applications a WHERE `+
		strings.Join(where, " AND ")+` ORDER BY a.last_activity_at DESC LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Application{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetApplication(ctx context.Context, userID string, id int64) (Application, []Event, error) {
	a, err := scanApp(s.db.QueryRowContext(ctx,
		`SELECT `+appColumns+` FROM applications a WHERE a.user_id = ? AND a.id = ?`, userID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil, ErrNotFound
	} else if err != nil {
		return a, nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, account, occurred_at, sender, subject, snippet, kind FROM events
		WHERE user_id = ? AND application_id = ? ORDER BY occurred_at DESC, id DESC`, userID, id)
	if err != nil {
		return a, nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&e.ID, &e.Account, &at, &e.Sender, &e.Subject, &e.Snippet, &e.Kind); err != nil {
			return a, nil, err
		}
		e.OccurredAt, _ = time.Parse(time.RFC3339, at)
		events = append(events, e)
	}
	return a, events, rows.Err()
}

// SetStage manually overrides an application's stage; an empty stage clears the override.
func (s *Store) SetStage(ctx context.Context, userID string, id int64, stage string) error {
	var v any
	if stage != "" {
		v = stage
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx,
		`UPDATE applications SET stage_override = ?, override_at = CASE WHEN ? IS NULL THEN NULL ELSE ? END
		 WHERE user_id = ? AND id = ?`, v, v, time.Now().UTC().Format(time.RFC3339), userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := rebuildHistory(ctx, tx, userID, id); err != nil {
		return err
	}
	return tx.Commit()
}

type Summary struct {
	Total        int            `json:"total"`
	ByStage      map[string]int `json:"by_stage"`
	NeedsNudge   []Application  `json:"needs_nudge"`
	RecentEvents []RecentEvent  `json:"recent_events"`
}

type RecentEvent struct {
	Event
	ApplicationID int64  `json:"application_id"`
	Company       string `json:"company"`
	Role          string `json:"role"`
}

// Summary powers the Today view. Applications still "applied" or "assessment" with no
// activity for quietDays are surfaced as follow-up candidates.
func (s *Store) Summary(ctx context.Context, userID string, now time.Time, quietDays int) (Summary, error) {
	sum := Summary{ByStage: map[string]int{}}
	for _, st := range Stages {
		sum.ByStage[st] = 0
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT COALESCE(stage_override, stage), COUNT(*) FROM applications WHERE user_id = ? GROUP BY 1`, userID)
	if err != nil {
		return sum, err
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			rows.Close()
			return sum, err
		}
		sum.ByStage[st] = n
		sum.Total += n
	}
	rows.Close()

	cutoff := now.AddDate(0, 0, -quietDays).UTC().Format(time.RFC3339)
	rows, err = s.db.QueryContext(ctx, `SELECT `+appColumns+` FROM applications a
		WHERE a.user_id = ? AND COALESCE(a.stage_override, a.stage) IN ('applied', 'assessment', 'interview')
		  AND a.last_activity_at < ?
		ORDER BY a.last_activity_at DESC LIMIT 5`, userID, cutoff)
	if err != nil {
		return sum, err
	}
	sum.NeedsNudge = []Application{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			rows.Close()
			return sum, err
		}
		sum.NeedsNudge = append(sum.NeedsNudge, a)
	}
	rows.Close()

	rows, err = s.db.QueryContext(ctx, `
		SELECT e.id, e.account, e.occurred_at, e.sender, e.subject, e.snippet, e.kind, a.id, a.company, a.role
		FROM events e JOIN applications a ON a.id = e.application_id
		WHERE e.user_id = ? ORDER BY e.occurred_at DESC LIMIT 8`, userID)
	if err != nil {
		return sum, err
	}
	defer rows.Close()
	sum.RecentEvents = []RecentEvent{}
	for rows.Next() {
		var r RecentEvent
		var at string
		if err := rows.Scan(&r.ID, &r.Account, &at, &r.Sender, &r.Subject, &r.Snippet, &r.Kind,
			&r.ApplicationID, &r.Company, &r.Role); err != nil {
			return sum, err
		}
		r.OccurredAt, _ = time.Parse(time.RFC3339, at)
		sum.RecentEvents = append(sum.RecentEvents, r)
	}
	return sum, rows.Err()
}

// Intent is stage 1 of the PRD: what the candidate is looking for.
type Intent struct {
	Roles        []string `json:"roles"`
	Designation  string   `json:"designation"`
	Locations    []string `json:"locations"`
	SalaryMin    int      `json:"salary_min"`
	SalaryMax    int      `json:"salary_max"`
	Currency     string   `json:"currency"`
	CompanyTypes []string `json:"company_types"`
	Situation    string   `json:"situation"`
}

var situations = map[string]bool{"": true, "exploring": true, "immediate": true, "reentry": true, "growth": true}

func (i Intent) Validate() error {
	if !situations[i.Situation] {
		return fmt.Errorf("invalid situation %q", i.Situation)
	}
	if i.SalaryMin < 0 || i.SalaryMax < 0 || (i.SalaryMax > 0 && i.SalaryMin > i.SalaryMax) {
		return errors.New("invalid salary range")
	}
	// Annual pay above ₹50 crore or $10M is a units mistake (rupees typed into an LPA field).
	if (i.Currency != "USD" && i.SalaryMax > 500_000_000) || (i.Currency == "USD" && i.SalaryMax > 10_000_000) {
		return errors.New("salary looks too large: enter lakhs per annum (e.g. 60), not rupees")
	}
	if len(i.Roles) > 10 || len(i.Locations) > 10 || len(i.CompanyTypes) > 10 {
		return errors.New("too many items")
	}
	for _, s := range append(append(append([]string{}, i.Roles...), i.Locations...), i.Designation) {
		if len(s) > 100 {
			return errors.New("value too long")
		}
	}
	return nil
}

func (s *Store) GetIntent(ctx context.Context, userID string) (Intent, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT data FROM intents WHERE user_id = ?`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Intent{Currency: "INR"}, nil
	} else if err != nil {
		return Intent{}, err
	}
	var i Intent
	return i, json.Unmarshal([]byte(raw), &i)
}

func (s *Store) PutIntent(ctx context.Context, userID string, i Intent, now time.Time) error {
	raw, err := json.Marshal(i)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO intents (user_id, data, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
		userID, string(raw), now.UTC().Format(time.RFC3339))
	return err
}
