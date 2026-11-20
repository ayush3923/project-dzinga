// Package store owns the startoff service's data (ADR 0011): company research with
// its full history (every profile version, every set of facts and news item from
// the daily quiz, every diagnosis), Claude's research queue, and a record of each
// daily run. Organizations are the organizations service's ids; their names are
// kept here for the queue. Every query is scoped by user ID.
package store

import (
	"context"
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
	// v1: research moved from the organizations service, now with history; daily runs.
	`
-- An organization this service holds research for (the organizations service's id).
CREATE TABLE companies (
	user_id           TEXT NOT NULL,
	org_id            INTEGER NOT NULL,
	name              TEXT NOT NULL,
	briefing_seen_at  TEXT,   -- when the user last opened it: clears "new from the quiz"
	diagnosis_seen_at TEXT,   -- and "new diagnosis"
	PRIMARY KEY (user_id, org_id)
);

-- Every company profile Claude has written; the newest is current.
CREATE TABLE profiles (
	id              INTEGER PRIMARY KEY,
	user_id         TEXT NOT NULL,
	org_id          INTEGER NOT NULL,
	overview        TEXT NOT NULL,
	interview_angle TEXT NOT NULL,
	model           TEXT NOT NULL,
	created_at      TEXT NOT NULL
);
CREATE INDEX profiles_org ON profiles (user_id, org_id, created_at);
CREATE TABLE profile_products (
	id          INTEGER PRIMARY KEY,
	profile_id  INTEGER NOT NULL REFERENCES profiles (id) ON DELETE CASCADE,
	position    INTEGER NOT NULL,
	name        TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT ''
);
CREATE TABLE profile_leaders (
	id         INTEGER PRIMARY KEY,
	profile_id INTEGER NOT NULL REFERENCES profiles (id) ON DELETE CASCADE,
	position   INTEGER NOT NULL,
	name       TEXT NOT NULL,
	title      TEXT NOT NULL DEFAULT ''
);
CREATE TABLE profile_ratings (
	id           INTEGER PRIMARY KEY,
	profile_id   INTEGER NOT NULL REFERENCES profiles (id) ON DELETE CASCADE,
	source       TEXT NOT NULL CHECK (source IN ('glassdoor', 'gartner_peer_insights')),
	score        REAL,                     -- NULL when not found
	scale        REAL NOT NULL DEFAULT 5,
	review_count INTEGER,
	url          TEXT NOT NULL DEFAULT '',
	as_of        TEXT NOT NULL DEFAULT ''
);

-- Facts from the daily quiz (leaders, revenue), a row each time they change.
-- Revenue figures are text as published ("$177.6B", "₹7,115 crore"), with the period they cover.
CREATE TABLE facts (
	id                     INTEGER PRIMARY KEY,
	user_id                TEXT NOT NULL,
	org_id                 INTEGER NOT NULL,
	ceo                    TEXT NOT NULL DEFAULT '',
	cpo                    TEXT NOT NULL DEFAULT '',
	revenue_year           TEXT NOT NULL DEFAULT '',
	revenue_year_period    TEXT NOT NULL DEFAULT '',
	revenue_year_url       TEXT NOT NULL DEFAULT '',
	revenue_quarter        TEXT NOT NULL DEFAULT '',
	revenue_quarter_period TEXT NOT NULL DEFAULT '',
	revenue_quarter_url    TEXT NOT NULL DEFAULT '',
	leaders_url            TEXT NOT NULL DEFAULT '',
	model                  TEXT NOT NULL,
	created_at             TEXT NOT NULL
);
CREATE INDEX facts_org ON facts (user_id, org_id, created_at);

-- Company news, all of it: items from a profile (origin 'profile', tied to that
-- version) and items the daily quiz found (origin 'quiz', each link once).
CREATE TABLE news (
	id           INTEGER PRIMARY KEY,
	user_id      TEXT NOT NULL,
	org_id       INTEGER NOT NULL,
	origin       TEXT NOT NULL CHECK (origin IN ('profile', 'quiz')),
	profile_id   INTEGER REFERENCES profiles (id) ON DELETE CASCADE,
	position     INTEGER NOT NULL DEFAULT 0,
	day          TEXT NOT NULL DEFAULT '',  -- the quiz day it was found for
	title        TEXT NOT NULL,
	summary      TEXT NOT NULL DEFAULT '',
	source_title TEXT NOT NULL DEFAULT '',
	url          TEXT NOT NULL DEFAULT '',
	published_on TEXT NOT NULL DEFAULT '',  -- YYYY-MM-DD when known
	created_at   TEXT NOT NULL
);
CREATE INDEX news_org ON news (user_id, org_id, origin);
CREATE UNIQUE INDEX news_quiz_once ON news (user_id, org_id, url) WHERE origin = 'quiz';

-- Every diagnosis; the newest is current.
CREATE TABLE diagnoses (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT NOT NULL,
	org_id     INTEGER NOT NULL,
	summary    TEXT NOT NULL,
	model      TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX diagnoses_org ON diagnoses (user_id, org_id, created_at);
CREATE TABLE diagnosis_items (
	id           INTEGER PRIMARY KEY,
	diagnosis_id INTEGER NOT NULL REFERENCES diagnoses (id) ON DELETE CASCADE,
	kind         TEXT NOT NULL CHECK (kind IN ('cause', 'action')),
	position     INTEGER NOT NULL,
	text         TEXT NOT NULL,
	confidence   TEXT NOT NULL DEFAULT '' CHECK (confidence IN ('high', 'medium', 'low', ''))
);

-- One row per daily run (the 07:00 timer, the Refresh button, or Claude), with its steps.
CREATE TABLE runs (
	id          INTEGER PRIMARY KEY,
	user_id     TEXT NOT NULL,
	day         TEXT NOT NULL,             -- the local day it ran for
	trigger     TEXT NOT NULL CHECK (trigger IN ('timer', 'manual', 'claude')),
	status      TEXT NOT NULL CHECK (status IN ('running', 'done', 'partial')),
	started_at  TEXT NOT NULL,
	finished_at TEXT
);
CREATE INDEX runs_day ON runs (user_id, day);
CREATE TABLE run_steps (
	id          INTEGER PRIMARY KEY,
	run_id      INTEGER NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
	position    INTEGER NOT NULL,
	name        TEXT NOT NULL,
	status      TEXT NOT NULL CHECK (status IN ('done', 'skipped', 'failed')),
	detail      TEXT NOT NULL DEFAULT '',
	finished_at TEXT NOT NULL
);

-- Research queued for Claude in the app (docs/runbooks/claude-queue.md).
CREATE TABLE research_requests (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT NOT NULL,
	org_id     INTEGER NOT NULL,
	kind       TEXT NOT NULL CHECK (kind IN ('company_profile', 'diagnosis')),
	status     TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
	run_id     INTEGER REFERENCES runs (id), -- the daily run that queued it, if any
	created_at TEXT NOT NULL,
	done_at    TEXT
);
CREATE UNIQUE INDEX research_one_pending ON research_requests (user_id, org_id, kind) WHERE status = 'pending';
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

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
