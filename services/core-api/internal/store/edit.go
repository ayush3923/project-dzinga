package store

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"unicode"
)

// ApplicationEdit holds the fields a user can correct; nil leaves a field unchanged.
type ApplicationEdit struct {
	Company    *string `json:"company"`
	Role       *string `json:"role"`
	JobURL     *string `json:"job_url"` // "" removes the link
	RoundsDone *int    `json:"rounds_done"`
	NextStep   *string `json:"next_step"` // "" removes it
}

func (e ApplicationEdit) Empty() bool {
	return e.Company == nil && e.Role == nil && e.JobURL == nil && e.RoundsDone == nil && e.NextStep == nil
}

// ValidJobURL accepts public https links only.
func ValidJobURL(u string) error {
	if u == "" {
		return nil
	}
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host == "" || p.User != nil || len(u) > 2000 {
		return errors.New("enter a public https link to the job description")
	}
	host := strings.ToLower(p.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") || host == "0.0.0.0" || strings.Contains(host, ":") {
		return errors.New("enter a public https link to the job description")
	}
	return nil
}

func (e ApplicationEdit) Validate() error {
	if e.Company != nil {
		c := strings.TrimSpace(*e.Company)
		if OrgKey(c) == "" || len(c) > 120 {
			return errors.New("the organization name needs letters or digits (at most 120 characters)")
		}
	}
	if e.Role != nil && len(strings.TrimSpace(*e.Role)) > 200 {
		return errors.New("the role is too long")
	}
	if e.RoundsDone != nil && (*e.RoundsDone < 0 || *e.RoundsDone > 20) {
		return errors.New("rounds done must be between 0 and 20")
	}
	if e.NextStep != nil {
		n := strings.TrimSpace(*e.NextStep)
		if len(n) > 120 || strings.ContainsFunc(n, unicode.IsControl) {
			return errors.New("the next step must be one line of at most 120 characters")
		}
	}
	if e.JobURL != nil {
		return ValidJobURL(strings.TrimSpace(*e.JobURL))
	}
	return nil
}

// EditApplication applies a user's corrections. Company and role re-key the
// application so future emails find it; when the corrected company and role match
// another application, the two are merged. Returns the application's (possibly new) id.
func (s *Store) EditApplication(ctx context.Context, userID string, id int64, e ApplicationEdit) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var company, role, key string
	err = tx.QueryRowContext(ctx, `SELECT company, role, key FROM applications WHERE user_id = ? AND id = ?`, userID, id).
		Scan(&company, &role, &key)
	if err != nil {
		return 0, ErrNotFound
	}
	newCompany, newRole := company, role
	if e.Company != nil {
		newCompany = strings.TrimSpace(*e.Company)
	}
	if e.Role != nil {
		newRole = strings.TrimSpace(*e.Role)
	}
	target := id
	if newCompany != company || newRole != role {
		newKey := AppKey(newCompany, newRole)
		if newRole == "" {
			if i := strings.Index(key, "@"); i >= 0 { // keep a role-less application's date suffix
				newKey += key[i:]
			}
		}
		var other int64
		_ = tx.QueryRowContext(ctx, `SELECT id FROM applications WHERE user_id = ? AND key = ? AND id != ?`, userID, newKey, id).Scan(&other)
		if other != 0 {
			// Merge into the existing application with that company and role.
			for _, q := range []string{
				`UPDATE events SET application_id = ? WHERE application_id = ?`,
				`UPDATE search_fts SET application_id = ? WHERE application_id = ?`,
				`UPDATE OR IGNORE contacts SET application_id = ? WHERE application_id = ?`,
				`UPDATE interview_notes SET application_id = ? WHERE application_id = ?`,
				`UPDATE interview_reviews SET application_id = ? WHERE application_id = ?`,
			} {
				if _, err := tx.ExecContext(ctx, q, other, id); err != nil {
					return 0, err
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE applications SET
				job_url = CASE WHEN job_url = '' THEN (SELECT job_url FROM applications WHERE id = ?) ELSE job_url END,
				next_step = CASE WHEN next_step = '' THEN (SELECT next_step FROM applications WHERE id = ?) ELSE next_step END,
				rounds_done = MAX(rounds_done, (SELECT rounds_done FROM applications WHERE id = ?))
				WHERE id = ?`, id, id, id, other); err != nil {
				return 0, err
			}
			// Pending research moves too; where the other application already has the
			// same request pending, the duplicate is dropped.
			if _, err := tx.ExecContext(ctx, `UPDATE OR IGNORE research_requests SET target_id = ?
				WHERE user_id = ? AND target_id = ? AND kind IN `+appKinds+` AND status = 'pending'`, other, userID, id); err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM research_requests
				WHERE user_id = ? AND target_id = ? AND kind IN `+appKinds+` AND status = 'pending'`, userID, id); err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM applications WHERE id = ?`, id); err != nil {
				return 0, err
			}
			target = other
		} else if _, err := tx.ExecContext(ctx, `UPDATE applications SET company = ?, role = ?, key = ? WHERE id = ?`,
			newCompany, newRole, newKey, id); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET
			applied_at       = COALESCE((SELECT MIN(occurred_at) FROM events WHERE application_id = ?), applied_at),
			last_activity_at = COALESCE((SELECT MAX(occurred_at) FROM events WHERE application_id = ?), last_activity_at),
			stage = COALESCE((SELECT kind FROM events WHERE application_id = ? AND kind != 'update'
				ORDER BY occurred_at DESC, id DESC LIMIT 1), stage)
			WHERE id = ?`, target, target, target, target); err != nil {
			return 0, err
		}
	}
	if e.JobURL != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET job_url = ? WHERE id = ?`, strings.TrimSpace(*e.JobURL), target); err != nil {
			return 0, err
		}
	}
	if e.RoundsDone != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET rounds_done = ? WHERE id = ?`, *e.RoundsDone, target); err != nil {
			return 0, err
		}
	}
	if e.NextStep != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE applications SET next_step = ? WHERE id = ?`, strings.TrimSpace(*e.NextStep), target); err != nil {
			return 0, err
		}
	}
	if err := rebuildHistory(ctx, tx, userID, target); err != nil {
		return 0, err
	}

	return target, tx.Commit()
}
