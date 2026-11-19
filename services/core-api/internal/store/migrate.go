package store

import (
	"database/sql"
	"fmt"
)

// migrations run in order; PRAGMA user_version records how many have been applied.
// The base `schema` (version 1) is always applied first with IF NOT EXISTS.
// The production PostgreSQL DDL mirrors these tables: docs/data-model.md.
var migrations = []string{
	// v2: application analysis — profile, postings, fit scores, stage history,
	// contacts, outreach drafts and LLM usage.
	`
ALTER TABLE events ADD COLUMN sender_name TEXT NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN job_url TEXT NOT NULL DEFAULT '';
ALTER TABLE applications ADD COLUMN override_at TEXT;

-- The candidate's profile, used for fit scoring and cold emails.
CREATE TABLE profiles (
	user_id          TEXT PRIMARY KEY,
	full_name        TEXT NOT NULL DEFAULT '',
	headline         TEXT NOT NULL DEFAULT '',
	current_title    TEXT NOT NULL DEFAULT '',
	current_company  TEXT NOT NULL DEFAULT '',
	total_years      REAL NOT NULL DEFAULT 0,
	location         TEXT NOT NULL DEFAULT '',
	summary          TEXT NOT NULL DEFAULT '',
	updated_at       TEXT NOT NULL
);
CREATE TABLE profile_experiences (
	id          INTEGER PRIMARY KEY,
	user_id     TEXT NOT NULL,
	position    INTEGER NOT NULL,          -- display order, 0 = most recent
	title       TEXT NOT NULL,
	company     TEXT NOT NULL,
	start_month TEXT NOT NULL DEFAULT '',  -- YYYY-MM
	end_month   TEXT NOT NULL DEFAULT '',  -- YYYY-MM, '' = present
	location    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_profile_exp_user ON profile_experiences (user_id, position);
CREATE TABLE profile_highlights (
	id            INTEGER PRIMARY KEY,
	experience_id INTEGER NOT NULL REFERENCES profile_experiences (id) ON DELETE CASCADE,
	position      INTEGER NOT NULL,
	text          TEXT NOT NULL
);
CREATE TABLE profile_skills (
	user_id TEXT NOT NULL,
	skill   TEXT NOT NULL,
	PRIMARY KEY (user_id, skill)
);

-- The job posting behind an application, found from the email link or a web search.
CREATE TABLE job_postings (
	id               INTEGER PRIMARY KEY,
	user_id          TEXT NOT NULL,
	application_id   INTEGER NOT NULL UNIQUE REFERENCES applications (id) ON DELETE CASCADE,
	status           TEXT NOT NULL CHECK (status IN ('found', 'not_found')),
	source           TEXT NOT NULL CHECK (source IN ('email_link', 'web_search', 'none')),
	url              TEXT NOT NULL DEFAULT '',
	title            TEXT NOT NULL DEFAULT '',
	location         TEXT NOT NULL DEFAULT '',
	work_mode        TEXT NOT NULL DEFAULT '',   -- onsite | hybrid | remote | ''
	seniority        TEXT NOT NULL DEFAULT '',
	min_years        INTEGER,
	product_context  TEXT NOT NULL DEFAULT '',   -- what the team or product does
	hiring_manager_name  TEXT NOT NULL DEFAULT '', -- only when the posting or an official page names them
	hiring_manager_title TEXT NOT NULL DEFAULT '',
	fetched_at       TEXT NOT NULL
);
CREATE TABLE job_posting_items (
	id         INTEGER PRIMARY KEY,
	posting_id INTEGER NOT NULL REFERENCES job_postings (id) ON DELETE CASCADE,
	kind       TEXT NOT NULL CHECK (kind IN ('requirement', 'qualification', 'responsibility')),
	position   INTEGER NOT NULL,
	text       TEXT NOT NULL
);
CREATE INDEX idx_posting_items ON job_posting_items (posting_id, kind, position);

-- A fit score per run (history kept); the latest run per application is current.
CREATE TABLE fit_scores (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT NOT NULL,
	application_id INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	total          INTEGER NOT NULL CHECK (total BETWEEN 0 AND 100),
	verdict        TEXT NOT NULL,
	model          TEXT NOT NULL,
	created_at     TEXT NOT NULL
);
CREATE INDEX idx_fit_app ON fit_scores (application_id, created_at DESC);
CREATE TABLE fit_factors (
	score_id  INTEGER NOT NULL REFERENCES fit_scores (id) ON DELETE CASCADE,
	factor    TEXT NOT NULL CHECK (factor IN ('experience', 'location', 'seniority', 'role_alignment', 'interest_intent')),
	score     INTEGER NOT NULL CHECK (score BETWEEN 0 AND 20),
	rationale TEXT NOT NULL,
	PRIMARY KEY (score_id, factor)
);
CREATE TABLE fit_suggestions (
	id       INTEGER PRIMARY KEY,
	score_id INTEGER NOT NULL REFERENCES fit_scores (id) ON DELETE CASCADE,
	position INTEGER NOT NULL,
	text     TEXT NOT NULL
);

-- Each application's path through the stages, rebuilt from its emails.
CREATE TABLE stage_history (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT NOT NULL,
	application_id INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	stage          TEXT NOT NULL CHECK (stage IN ('applied', 'shortlisted', 'assessment', 'interview', 'offer', 'rejected')),
	round          INTEGER NOT NULL DEFAULT 0,   -- interview round, 1-based; 0 for other stages
	entered_at     TEXT NOT NULL,
	event_id       INTEGER REFERENCES events (id) ON DELETE SET NULL,
	source         TEXT NOT NULL CHECK (source IN ('email', 'manual'))
);
CREATE INDEX idx_stage_history_app ON stage_history (application_id, entered_at);
CREATE INDEX idx_stage_history_user ON stage_history (user_id, stage);

-- People at the company who emailed the user (never guessed addresses).
CREATE TABLE contacts (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT NOT NULL,
	application_id INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	name           TEXT NOT NULL DEFAULT '',
	email          TEXT NOT NULL DEFAULT '',
	role_hint      TEXT NOT NULL DEFAULT '',   -- recruiter | hiring manager | interviewer | ''
	source         TEXT NOT NULL CHECK (source IN ('mailbox', 'posting')),
	first_seen_at  TEXT NOT NULL,
	last_seen_at   TEXT NOT NULL,
	UNIQUE (user_id, application_id, email, name)
);

-- Cold emails drafted on request. Never sent by Dzinga.
CREATE TABLE outreach_drafts (
	id               INTEGER PRIMARY KEY,
	user_id          TEXT NOT NULL,
	application_id   INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	contact_id       INTEGER REFERENCES contacts (id) ON DELETE SET NULL,
	subject          TEXT NOT NULL,
	body             TEXT NOT NULL,
	product_analysis TEXT NOT NULL,
	model            TEXT NOT NULL,
	created_at       TEXT NOT NULL
);
CREATE TABLE outreach_points (
	id       INTEGER PRIMARY KEY,
	draft_id INTEGER NOT NULL REFERENCES outreach_drafts (id) ON DELETE CASCADE,
	kind     TEXT NOT NULL CHECK (kind IN ('experience', 'value_add', 'improvement')),
	position INTEGER NOT NULL,
	text     TEXT NOT NULL
);

-- Every model call, for cost and usage tracking (Rule 7).
CREATE TABLE llm_usage (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT NOT NULL,
	application_id INTEGER,
	purpose        TEXT NOT NULL,
	model          TEXT NOT NULL,
	input_tokens   INTEGER NOT NULL,
	output_tokens  INTEGER NOT NULL,
	web_searches   INTEGER NOT NULL DEFAULT 0,
	created_at     TEXT NOT NULL
);
`,
}

// v3: the model router's correlation stage and per-stage models.
var _ = appendMigration(`
ALTER TABLE fit_scores ADD COLUMN pipeline TEXT NOT NULL DEFAULT '';
CREATE TABLE fit_evidence (
	id          INTEGER PRIMARY KEY,
	score_id    INTEGER NOT NULL REFERENCES fit_scores (id) ON DELETE CASCADE,
	position    INTEGER NOT NULL,
	requirement TEXT NOT NULL,
	evidence    TEXT NOT NULL,
	strength    TEXT NOT NULL CHECK (strength IN ('strong', 'partial', 'missing'))
);
CREATE TABLE fit_points (
	id       INTEGER PRIMARY KEY,
	score_id INTEGER NOT NULL REFERENCES fit_scores (id) ON DELETE CASCADE,
	kind     TEXT NOT NULL CHECK (kind IN ('strength', 'gap')),
	position INTEGER NOT NULL,
	text     TEXT NOT NULL
);
`)

// v4: organizations, preferences, company profiles and diagnoses.
var _ = appendMigration(`
CREATE TABLE organizations (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT NOT NULL,
	key        TEXT NOT NULL,          -- lower-case letters and digits of the name
	name       TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (user_id, key)
);
ALTER TABLE applications ADD COLUMN organization_id INTEGER REFERENCES organizations (id);
CREATE INDEX idx_apps_org ON applications (organization_id);

-- 'auto' = top 3 by number of applications; 'manual' = the user's own ordered list.
CREATE TABLE org_settings (
	user_id        TEXT PRIMARY KEY,
	preferred_mode TEXT NOT NULL CHECK (preferred_mode IN ('auto', 'manual'))
);
CREATE TABLE org_preferences (
	user_id         TEXT NOT NULL,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	position        INTEGER NOT NULL,
	PRIMARY KEY (user_id, organization_id)
);

CREATE TABLE company_profiles (
	organization_id INTEGER PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
	user_id         TEXT NOT NULL,
	overview        TEXT NOT NULL,
	interview_angle TEXT NOT NULL,
	model           TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE TABLE company_news (
	id              INTEGER PRIMARY KEY,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	position        INTEGER NOT NULL,
	title           TEXT NOT NULL,
	published_on    TEXT NOT NULL DEFAULT '',  -- YYYY-MM-DD when known
	summary         TEXT NOT NULL DEFAULT '',
	url             TEXT NOT NULL DEFAULT ''
);
CREATE TABLE company_products (
	id              INTEGER PRIMARY KEY,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	position        INTEGER NOT NULL,
	name            TEXT NOT NULL,
	description     TEXT NOT NULL DEFAULT ''
);
CREATE TABLE company_leaders (
	id              INTEGER PRIMARY KEY,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	position        INTEGER NOT NULL,
	name            TEXT NOT NULL,
	title           TEXT NOT NULL DEFAULT ''
);
CREATE TABLE company_ratings (
	id              INTEGER PRIMARY KEY,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	source          TEXT NOT NULL CHECK (source IN ('glassdoor', 'gartner_peer_insights')),
	score           REAL,                     -- NULL when not found
	scale           REAL NOT NULL DEFAULT 5,
	review_count    INTEGER,
	url             TEXT NOT NULL DEFAULT '',
	as_of           TEXT NOT NULL DEFAULT ''
);

CREATE TABLE org_diagnoses (
	id              INTEGER PRIMARY KEY,
	user_id         TEXT NOT NULL,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	summary         TEXT NOT NULL,
	model           TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE TABLE org_diagnosis_items (
	id           INTEGER PRIMARY KEY,
	diagnosis_id INTEGER NOT NULL REFERENCES org_diagnoses (id) ON DELETE CASCADE,
	kind         TEXT NOT NULL CHECK (kind IN ('cause', 'action')),
	position     INTEGER NOT NULL,
	text         TEXT NOT NULL,
	confidence   TEXT NOT NULL DEFAULT '' CHECK (confidence IN ('high', 'medium', 'low', ''))
);
`)

// v5: connected mailboxes and sync settings. Credentials are NOT stored here: they
// live in the macOS Keychain (local) or AWS Secrets Manager (production).
var _ = appendMigration(`
CREATE TABLE mailboxes (
	id               INTEGER PRIMARY KEY,
	user_id          TEXT NOT NULL,
	address          TEXT NOT NULL,
	provider         TEXT NOT NULL CHECK (provider IN ('yahoo', 'gmail', 'icloud', 'microsoft')),
	enabled          INTEGER NOT NULL DEFAULT 1,  -- include in sync
	credential_saved INTEGER NOT NULL DEFAULT 0,  -- set by the ingest service
	last_synced_at   TEXT,
	last_status      TEXT NOT NULL DEFAULT 'never' CHECK (last_status IN ('never', 'ok', 'error')),
	last_error       TEXT NOT NULL DEFAULT '',
	last_added       INTEGER NOT NULL DEFAULT 0,
	created_at       TEXT NOT NULL,
	UNIQUE (user_id, address)
);
CREATE TABLE sync_settings (
	user_id    TEXT PRIMARY KEY,
	lookback   TEXT NOT NULL CHECK (lookback IN ('since_last', '14d', '1m', '2m', '3m', '6m', '12m', 'custom')),
	since_date TEXT NOT NULL DEFAULT ''   -- YYYY-MM-DD, for 'custom'
);
`)

// v6: a job-posting link the user adds to an application by hand.
var _ = appendMigration(`
ALTER TABLE applications ADD COLUMN job_url TEXT NOT NULL DEFAULT '';
`)

// v7: research requests (see research.go).
var _ = appendMigration(`
CREATE TABLE research_requests (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT    NOT NULL,
	kind       TEXT    NOT NULL CHECK (kind IN ('company_profile', 'job_posting')),
	target_id  INTEGER NOT NULL,
	status     TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
	created_at TEXT    NOT NULL,
	done_at    TEXT
);
CREATE UNIQUE INDEX research_requests_one_pending ON research_requests (user_id, kind, target_id) WHERE status = 'pending';
`)

// v8: when each email was pulled in, a log of refreshes, and digest texts (see digest.go).
var _ = appendMigration(`
ALTER TABLE events ADD COLUMN ingested_at TEXT NOT NULL DEFAULT '';
CREATE TABLE refreshes (
	id      INTEGER PRIMARY KEY,
	user_id TEXT    NOT NULL,
	at      TEXT    NOT NULL,
	added   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX refreshes_user_at ON refreshes (user_id, at DESC);
CREATE TABLE digests (
	user_id    TEXT    NOT NULL,
	refresh_id INTEGER NOT NULL REFERENCES refreshes (id) ON DELETE CASCADE,
	text       TEXT    NOT NULL,
	model      TEXT    NOT NULL,
	created_at TEXT    NOT NULL,
	PRIMARY KEY (user_id, refresh_id)
);
`)

// v9: interview progress the user records on an application (see edit.go)
var _ = appendMigration(`
ALTER TABLE applications ADD COLUMN rounds_done INTEGER NOT NULL DEFAULT 0;
ALTER TABLE applications ADD COLUMN next_step TEXT NOT NULL DEFAULT '';
`)

// v10: Claude's work queue gains fit scores and the daily quiz (see research.go and
// quiz.go). SQLite can't change a CHECK constraint, so the queue table is rebuilt.
var _ = appendMigration(`
CREATE TABLE research_requests_v10 (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT    NOT NULL,
	kind       TEXT    NOT NULL CHECK (kind IN ('company_profile', 'job_posting', 'fit_score', 'daily_quiz', 'quiz_feedback')),
	target_id  INTEGER NOT NULL,
	status     TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
	created_at TEXT    NOT NULL,
	done_at    TEXT
);
INSERT INTO research_requests_v10 (id, user_id, kind, target_id, status, created_at, done_at)
	SELECT id, user_id, kind, target_id, status, created_at, done_at FROM research_requests;
DROP TABLE research_requests;
ALTER TABLE research_requests_v10 RENAME TO research_requests;
CREATE UNIQUE INDEX research_requests_one_pending ON research_requests (user_id, kind, target_id) WHERE status = 'pending';

CREATE TABLE quizzes (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT NOT NULL,
	day        TEXT NOT NULL, -- YYYY-MM-DD (IST)
	model      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (user_id, day)
);
CREATE TABLE quiz_questions (
	id           INTEGER PRIMARY KEY,
	quiz_id      INTEGER NOT NULL REFERENCES quizzes (id) ON DELETE CASCADE,
	position     INTEGER NOT NULL,
	kind         TEXT    NOT NULL CHECK (kind IN ('mcq', 'subjective', 'voice')),
	topic        TEXT    NOT NULL CHECK (topic IN ('ai_news', 'pm_concept', 'org_news')),
	prompt       TEXT    NOT NULL,
	options      TEXT    NOT NULL DEFAULT '[]', -- JSON array, MCQ only
	answer       INTEGER,                       -- MCQ: index of the correct option
	explanation  TEXT    NOT NULL DEFAULT '',
	source_title TEXT    NOT NULL DEFAULT '',
	source_url   TEXT    NOT NULL DEFAULT '',
	UNIQUE (quiz_id, position)
);
CREATE TABLE quiz_answers (
	question_id INTEGER PRIMARY KEY REFERENCES quiz_questions (id) ON DELETE CASCADE,
	choice      INTEGER,
	text        TEXT    NOT NULL DEFAULT '',
	audio_file  TEXT    NOT NULL DEFAULT '', -- file name in the audio folder, never a path
	transcript  TEXT    NOT NULL DEFAULT '',
	feedback    TEXT    NOT NULL DEFAULT '',
	score       INTEGER,                     -- 0-10, from Claude's feedback
	answered_at TEXT    NOT NULL,
	feedback_at TEXT
);
`)

// v11: the daily quiz moved to its own service (ADR 0008), with its own database.
var _ = appendMigration(`
DELETE FROM research_requests WHERE kind IN ('daily_quiz', 'quiz_feedback');
DROP TABLE quiz_answers;
DROP TABLE quiz_questions;
DROP TABLE quizzes;
`)

// v12: the profile photo (see photo.go). The image is a file; this records that it exists.
var _ = appendMigration(`
CREATE TABLE profile_photos (
	user_id    TEXT PRIMARY KEY,
	updated_at TEXT NOT NULL
);
`)

// v13: organization briefings from the daily quiz (briefing.go), and the user's
// interview notes with Claude's review (interview.go). The queue gains
// interview_review; SQLite can't change a CHECK constraint, so it is rebuilt.
var _ = appendMigration(`
CREATE TABLE research_requests_v13 (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT    NOT NULL,
	kind       TEXT    NOT NULL CHECK (kind IN ('company_profile', 'job_posting', 'fit_score', 'interview_review')),
	target_id  INTEGER NOT NULL,
	status     TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
	created_at TEXT    NOT NULL,
	done_at    TEXT
);
INSERT INTO research_requests_v13 (id, user_id, kind, target_id, status, created_at, done_at)
	SELECT id, user_id, kind, target_id, status, created_at, done_at FROM research_requests;
DROP TABLE research_requests;
ALTER TABLE research_requests_v13 RENAME TO research_requests;
CREATE UNIQUE INDEX research_requests_one_pending ON research_requests (user_id, kind, target_id) WHERE status = 'pending';

-- Revenue figures are text as published ("$177.6B", "₹7,115 crore"), with the period they cover.
CREATE TABLE org_briefings (
	organization_id        INTEGER PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
	user_id                TEXT NOT NULL,
	ceo                    TEXT NOT NULL DEFAULT '',
	cpo                    TEXT NOT NULL DEFAULT '', -- chief product officer, or the nearest product head
	revenue_year           TEXT NOT NULL DEFAULT '',
	revenue_year_period    TEXT NOT NULL DEFAULT '',
	revenue_year_url       TEXT NOT NULL DEFAULT '',
	revenue_quarter        TEXT NOT NULL DEFAULT '',
	revenue_quarter_period TEXT NOT NULL DEFAULT '',
	revenue_quarter_url    TEXT NOT NULL DEFAULT '',
	leaders_url            TEXT NOT NULL DEFAULT '',
	model                  TEXT NOT NULL,
	updated_at             TEXT NOT NULL,
	seen_at                TEXT                       -- when the user last opened it
);
CREATE TABLE org_briefing_news (
	id              INTEGER PRIMARY KEY,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	day             TEXT NOT NULL,                -- the quiz day it was found for
	title           TEXT NOT NULL,
	summary         TEXT NOT NULL DEFAULT '',
	source_title    TEXT NOT NULL DEFAULT '',
	url             TEXT NOT NULL,
	published_on    TEXT NOT NULL DEFAULT '',     -- YYYY-MM-DD when known
	UNIQUE (organization_id, url)
);

CREATE TABLE interview_notes (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT    NOT NULL,
	application_id INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	round          INTEGER NOT NULL DEFAULT 0,   -- 0 when not given
	body           TEXT    NOT NULL,
	created_at     TEXT    NOT NULL
);
CREATE INDEX idx_interview_notes_app ON interview_notes (application_id);
CREATE TABLE interview_reviews (
	id             INTEGER PRIMARY KEY,
	user_id        TEXT    NOT NULL,
	application_id INTEGER NOT NULL REFERENCES applications (id) ON DELETE CASCADE,
	chance         TEXT    NOT NULL CHECK (chance IN ('low', 'medium', 'high')),
	summary        TEXT    NOT NULL,
	signals        TEXT    NOT NULL DEFAULT '[]', -- JSON: what points towards selection
	concerns       TEXT    NOT NULL DEFAULT '[]', -- JSON: what points against it
	next_steps     TEXT    NOT NULL DEFAULT '[]', -- JSON: what to do before the next round
	model          TEXT    NOT NULL,
	created_at     TEXT    NOT NULL
);
CREATE INDEX idx_interview_reviews_app ON interview_reviews (application_id);
`)

// Migrations are listed here, in order, and nowhere else: Go initialises files
// alphabetically, so a migration declared in another file could run out of order.

func appendMigration(m string) bool {
	migrations = append(migrations, m)
	return true
}

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version == 0 {
		version = 1 // base schema just applied
	}
	for i := version - 1; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("v%d: %w", i+2, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+2)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
