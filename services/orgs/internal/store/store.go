// Package store owns the organizations service's data: organizations, the user's
// preferred list, company profiles, diagnoses, briefings from the daily quiz, and
// the company-research queue. Applications stay in core-api; the views here are
// computed from core-api's analysis on each request. Every query is scoped by user ID.
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
`

// Migrations run in order; PRAGMA user_version records how many have been applied.
// Each keeps its position forever: existing databases are upgraded by number.
var migrations = []string{
	// v1: everything core-api held about organizations (its migrations 4, 5, 10 and 13), unchanged.
	`
CREATE TABLE organizations (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT NOT NULL,
	key        TEXT NOT NULL,          -- lower-case letters and digits of the name
	name       TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE (user_id, key)
);

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

-- Company research queued for Claude in the app (docs/runbooks/claude-queue.md).
CREATE TABLE research_requests (
	id              INTEGER PRIMARY KEY,
	user_id         TEXT NOT NULL,
	organization_id INTEGER NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
	status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
	created_at      TEXT NOT NULL,
	done_at         TEXT
);
CREATE UNIQUE INDEX research_requests_one_pending ON research_requests (user_id, organization_id) WHERE status = 'pending';
`,
	// v2: the queue also holds diagnoses (5 random organizations each refresh), and a
	// new diagnosis is flagged on the list until the user opens it.
	`
ALTER TABLE research_requests ADD COLUMN kind TEXT NOT NULL DEFAULT 'company_profile' CHECK (kind IN ('company_profile', 'diagnosis'));
DROP INDEX research_requests_one_pending;
CREATE UNIQUE INDEX research_requests_one_pending ON research_requests (user_id, organization_id, kind) WHERE status = 'pending';
ALTER TABLE org_diagnoses ADD COLUMN seen_at TEXT;
UPDATE org_diagnoses SET seen_at = created_at; -- diagnoses from before this change aren't new
`,
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer.
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

func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("v%d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
