// Package store owns the quiz service's data: daily quizzes, their questions, the
// user's answers and Claude's feedback. Every query is scoped by user ID.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

// IST: the user's day boundaries.
var IST = time.FixedZone("IST", 5*3600+1800)

type Store struct{ db *sql.DB }

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
`

// Migrations run in order; PRAGMA user_version records how many have been applied.
// Each keeps its position forever: existing databases are upgraded by number.
var migrations = []string{
	// v1: quizzes, questions, answers, and requests from refreshes.
	`
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
-- A refresh asks for the day's quiz; it's done when the quiz is saved.
CREATE TABLE quiz_requests (
	user_id      TEXT NOT NULL,
	day          TEXT NOT NULL,
	requested_at TEXT NOT NULL,
	done_at      TEXT,
	PRIMARY KEY (user_id, day)
);
`,
	// v2: the day's key news, gathered with the quiz and shown on the profile.
	`
CREATE TABLE news_items (
	id           INTEGER PRIMARY KEY,
	user_id      TEXT    NOT NULL,
	day          TEXT    NOT NULL, -- YYYY-MM-DD (IST): the quiz day it was gathered for
	position     INTEGER NOT NULL,
	topic        TEXT    NOT NULL CHECK (topic IN ('ai_news', 'org_news')),
	company      TEXT    NOT NULL DEFAULT '', -- org_news: the company it is about
	title        TEXT    NOT NULL,
	summary      TEXT    NOT NULL,
	source_title TEXT    NOT NULL,
	url          TEXT    NOT NULL,
	published_on TEXT    NOT NULL DEFAULT '', -- YYYY-MM-DD when known
	UNIQUE (user_id, day, position)
);
`,
	// v3: three bonus prep questions a day, aimed at the user's live interviews.
	`
CREATE TABLE prep_questions (
	id         INTEGER PRIMARY KEY,
	user_id    TEXT    NOT NULL,
	day        TEXT    NOT NULL, -- YYYY-MM-DD (IST)
	position   INTEGER NOT NULL,
	company    TEXT    NOT NULL DEFAULT '', -- '' for a general question
	role       TEXT    NOT NULL DEFAULT '',
	question   TEXT    NOT NULL,
	why        TEXT    NOT NULL,            -- why it is likely to come up
	approach   TEXT    NOT NULL,            -- how to structure a strong answer
	UNIQUE (user_id, day, position)
);
`,
	// v4: improvement drills. Feedback on a written or spoken answer can set a few
	// specific things to do next time; feedback on a later answer checks each one.
	`
CREATE TABLE drills (
	id                  INTEGER PRIMARY KEY,
	user_id             TEXT    NOT NULL,
	from_question_id    INTEGER NOT NULL REFERENCES quiz_questions (id) ON DELETE CASCADE,
	day                 TEXT    NOT NULL,  -- the quiz day of the answer it came from (IST)
	kind                TEXT    NOT NULL CHECK (kind IN ('subjective', 'voice', 'any')),
	position            INTEGER NOT NULL,
	text                TEXT    NOT NULL,
	status              TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'met', 'partly', 'missed')),
	checked_question_id INTEGER REFERENCES quiz_questions (id) ON DELETE SET NULL,
	checked_day         TEXT,              -- the quiz day of the answer it was checked against
	check_note          TEXT    NOT NULL DEFAULT '',
	created_at          TEXT    NOT NULL,
	checked_at          TEXT
);
CREATE INDEX drills_open ON drills (user_id, status);
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

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
