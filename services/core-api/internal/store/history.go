package store

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
	"time"
)

// Follow-up emails about an interview already counted: reminders, reschedules,
// confirmations, NDAs. They don't start a new round.
var sameRound = regexp.MustCompile(`(?i)remind|reschedul|response requested|confirm|completed|accepted:|updated invitation|cancel`)

type histEvent struct {
	id      int64
	at      time.Time
	kind    string
	subject string
}

type step struct {
	stage   string
	round   int
	at      time.Time
	eventID int64
	source  string
}

// deriveSteps turns an application's emails (oldest first) into its stage path:
// applied → shortlisted → assessment → interview 1, 2, … → offer | rejected.
// "Shortlisted" is when the company first moved the user forward (an assessment or
// interview invite). A new interview round starts on an invite that isn't a
// reminder or reschedule and comes at least 2 days after the previous round began.
func deriveSteps(evs []histEvent) []step {
	var out []step
	if len(evs) == 0 {
		return out
	}
	// A new application email after a decision (e.g. applying to another role at the
	// same company) starts a new cycle; the timeline follows the latest one.
	decidedAt := -1
	for i, e := range evs {
		if e.kind == "offer" || e.kind == "rejected" {
			decidedAt = i
		} else if e.kind == "applied" && decidedAt >= 0 {
			evs, decidedAt = evs[i:], -1
			return deriveSteps(evs)
		}
	}
	out = append(out, step{stage: "applied", at: evs[0].at, eventID: evs[0].id, source: "email"})
	var shortlisted, assessed, decided bool
	var lastRound time.Time
	round := 0
	for _, e := range evs {
		if decided {
			break
		}
		switch e.kind {
		case "assessment", "interview":
			if !shortlisted {
				shortlisted = true
				out = append(out, step{stage: "shortlisted", at: e.at, eventID: e.id, source: "email"})
			}
			if e.kind == "assessment" && !assessed {
				assessed = true
				out = append(out, step{stage: "assessment", at: e.at, eventID: e.id, source: "email"})
			}
			if e.kind == "interview" {
				newRound := round == 0 || (!sameRound.MatchString(e.subject) && e.at.Sub(lastRound) >= 48*time.Hour)
				if newRound {
					round++
					lastRound = e.at
					out = append(out, step{stage: "interview", round: round, at: e.at, eventID: e.id, source: "email"})
				}
			}
		case "offer", "rejected":
			decided = true
			out = append(out, step{stage: e.kind, at: e.at, eventID: e.id, source: "email"})
		}
	}
	return out
}

func rebuildHistory(ctx context.Context, tx *sql.Tx, userID string, appID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, occurred_at, kind, subject FROM events
		WHERE user_id = ? AND application_id = ? ORDER BY occurred_at, id`, userID, appID)
	if err != nil {
		return err
	}
	var evs []histEvent
	for rows.Next() {
		var e histEvent
		var at string
		if err := rows.Scan(&e.id, &at, &e.kind, &e.subject); err != nil {
			rows.Close()
			return err
		}
		e.at, _ = time.Parse(time.RFC3339, at)
		evs = append(evs, e)
	}
	rows.Close()
	steps := deriveSteps(evs)

	// A manual stage the emails don't show is appended as the latest step.
	var override, overrideAt sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT stage_override, override_at FROM applications WHERE id = ?`, appID).
		Scan(&override, &overrideAt); err != nil {
		return err
	}
	if override.Valid && override.String != "" && (len(steps) == 0 || steps[len(steps)-1].stage != override.String) {
		at, _ := time.Parse(time.RFC3339, overrideAt.String)
		st := step{stage: override.String, at: at, source: "manual"}
		if st.stage == "interview" {
			st.round = 1
			for _, s := range steps {
				if s.stage == "interview" {
					st.round = s.round + 1
				}
			}
		}
		steps = append(steps, st)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM stage_history WHERE application_id = ?`, appID); err != nil {
		return err
	}
	for _, s := range steps {
		var ev any
		if s.eventID != 0 {
			ev = s.eventID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stage_history
			(user_id, application_id, stage, round, entered_at, event_id, source) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			userID, appID, s.stage, s.round, s.at.UTC().Format(time.RFC3339), ev, s.source); err != nil {
			return err
		}
	}
	return nil
}

var automatedSender = regexp.MustCompile(`(?i)^(no-?reply|do-?not-?reply|notifications?|alerts?|jobs|careers|recruiting|talent|hr|info|support|hello|team|mailer|bounce|workday|greenhouse)[^@]*@`)

// upsertContact records a person (not a robot address) who emailed the user about
// this application. Addresses are only ever taken from received mail.
func upsertContact(ctx context.Context, tx *sql.Tx, userID string, appID int64, e IngestEvent) error {
	if e.Sender == "" || automatedSender.MatchString(e.Sender) || strings.Contains(e.SenderName, "@") {
		return nil
	}
	role := ""
	switch e.Kind {
	case "interview":
		role = "interviewer or recruiter"
	case "assessment", "applied", "rejected", "offer", "update":
		role = "recruiter"
	}
	at := e.OccurredAt.UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `
		INSERT INTO contacts (user_id, application_id, name, email, role_hint, source, first_seen_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, 'mailbox', ?, ?)
		ON CONFLICT (user_id, application_id, email, name) DO UPDATE SET
			last_seen_at  = MAX(last_seen_at, excluded.last_seen_at),
			first_seen_at = MIN(first_seen_at, excluded.first_seen_at)`,
		userID, appID, e.SenderName, strings.ToLower(e.Sender), role, at, at)
	return err
}

// RebuildAllHistory recomputes every application's stage path (after a migration,
// or when the derivation rules change).
func (s *Store) RebuildAllHistory(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id, user_id FROM applications`)
	if err != nil {
		return err
	}
	type app struct {
		id   int64
		user string
	}
	var apps []app
	for rows.Next() {
		var a app
		if err := rows.Scan(&a.id, &a.user); err != nil {
			rows.Close()
			return err
		}
		apps = append(apps, a)
	}
	rows.Close()
	for _, a := range apps {
		if err := rebuildHistory(ctx, tx, a.user, a.id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
