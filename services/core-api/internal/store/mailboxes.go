package store

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"
)

type Mailbox struct {
	ID              int64      `json:"id"`
	Address         string     `json:"address"`
	Provider        string     `json:"provider"` // yahoo | gmail | icloud | microsoft
	Enabled         bool       `json:"enabled"`
	CredentialSaved bool       `json:"credential_saved"`
	LastSyncedAt    *time.Time `json:"last_synced_at"`
	LastStatus      string     `json:"last_status"` // never | ok | error
	LastError       string     `json:"last_error"`
	LastAdded       int        `json:"last_added"`
	Emails          int        `json:"emails"` // job emails stored from this mailbox
}

type SyncSettings struct {
	Lookback  string `json:"lookback"`   // since_last | 14d | 1m | 2m | 3m | 6m | 12m | custom
	SinceDate string `json:"since_date"` // YYYY-MM-DD when custom
}

var lookbacks = map[string]bool{"since_last": true, "14d": true, "1m": true, "2m": true, "3m": true, "6m": true, "12m": true, "custom": true}

func (s SyncSettings) Validate() error {
	if !lookbacks[s.Lookback] {
		return errors.New("invalid lookback")
	}
	if s.Lookback == "custom" {
		if _, err := time.Parse("2006-01-02", s.SinceDate); err != nil {
			return errors.New("since_date must be YYYY-MM-DD")
		}
	}
	return nil
}

// ProviderFor guesses the mail provider from an address.
func ProviderFor(address string) string {
	domain := strings.ToLower(address[strings.LastIndex(address, "@")+1:])
	base := strings.Split(domain, ".")[0]
	switch base {
	case "yahoo", "ymail", "rocketmail":
		return "yahoo"
	case "gmail", "googlemail":
		return "gmail"
	case "icloud", "me", "mac":
		return "icloud"
	case "outlook", "live", "hotmail", "msn":
		return "microsoft"
	}
	return ""
}

var ErrBadAddress = errors.New("enter a Yahoo, Gmail, iCloud, Outlook, Hotmail or Live address")

func normalizeAddress(a string) (string, string, error) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(a))
	if err != nil || parsed.Name != "" || len(parsed.Address) > 254 {
		return "", "", ErrBadAddress
	}
	addr := strings.ToLower(parsed.Address)
	p := ProviderFor(addr)
	if p == "" {
		return "", "", ErrBadAddress
	}
	return addr, p, nil
}

// SeedMailboxes registers every mailbox that has delivered job emails.
func (s *Store) SeedMailboxes(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT user_id, account FROM events`)
	if err != nil {
		return err
	}
	type acct struct{ user, addr string }
	var accts []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.user, &a.addr); err != nil {
			rows.Close()
			return err
		}
		accts = append(accts, a)
	}
	rows.Close()
	for _, a := range accts {
		addr, p, err := normalizeAddress(a.addr)
		if err != nil {
			continue // e.g. the demo mailbox
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO mailboxes (user_id, address, provider, created_at) VALUES (?, ?, ?, ?)`,
			a.user, addr, p, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Mailboxes(ctx context.Context, userID string) ([]Mailbox, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.id, m.address, m.provider, m.enabled, m.credential_saved, m.last_synced_at,
		m.last_status, m.last_error, m.last_added,
		(SELECT COUNT(*) FROM events e WHERE e.user_id = m.user_id AND lower(e.account) = m.address)
		FROM mailboxes m WHERE m.user_id = ? ORDER BY m.provider, m.address`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mailbox{}
	for rows.Next() {
		var m Mailbox
		var last sql.NullString
		if err := rows.Scan(&m.ID, &m.Address, &m.Provider, &m.Enabled, &m.CredentialSaved, &last,
			&m.LastStatus, &m.LastError, &m.LastAdded, &m.Emails); err != nil {
			return nil, err
		}
		if last.Valid {
			t, _ := time.Parse(time.RFC3339, last.String)
			m.LastSyncedAt = &t
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddMailbox(ctx context.Context, userID, address string, now time.Time) (Mailbox, error) {
	addr, p, err := normalizeAddress(address)
	if err != nil {
		return Mailbox{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO mailboxes (user_id, address, provider, created_at) VALUES (?, ?, ?, ?)`,
		userID, addr, p, now.UTC().Format(time.RFC3339)); err != nil {
		return Mailbox{}, err
	}
	all, err := s.Mailboxes(ctx, userID)
	if err != nil {
		return Mailbox{}, err
	}
	for _, m := range all {
		if m.Address == addr {
			return m, nil
		}
	}
	return Mailbox{}, ErrNotFound
}

func (s *Store) SetMailboxEnabled(ctx context.Context, userID string, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE mailboxes SET enabled = ? WHERE user_id = ? AND id = ?`, enabled, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveMailbox forgets a mailbox. Its imported emails stay; the ingest service
// deletes the stored credential before calling this.
func (s *Store) RemoveMailbox(ctx context.Context, userID string, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM mailboxes WHERE user_id = ? AND id = ?`, userID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MailboxState is what the ingest service reports after connecting or syncing.
type MailboxState struct {
	CredentialSaved *bool      `json:"credential_saved"`
	SyncedAt        *time.Time `json:"synced_at"`
	Status          string     `json:"status"` // ok | error | "" (unchanged)
	Error           string     `json:"error"`
	Added           *int       `json:"added"`
}

func (s *Store) SetMailboxState(ctx context.Context, userID string, id int64, st MailboxState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM mailboxes WHERE user_id = ? AND id = ?`, userID, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if st.CredentialSaved != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET credential_saved = ? WHERE id = ?`, *st.CredentialSaved, id); err != nil {
			return err
		}
	}
	if st.Status == "ok" || st.Status == "error" {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET last_status = ?, last_error = ? WHERE id = ?`, st.Status, truncate(st.Error, 500), id); err != nil {
			return err
		}
	}
	if st.SyncedAt != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET last_synced_at = ? WHERE id = ?`, st.SyncedAt.UTC().Format(time.RFC3339), id); err != nil {
			return err
		}
	}
	if st.Added != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE mailboxes SET last_added = ? WHERE id = ?`, *st.Added, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetSyncSettings(ctx context.Context, userID string) (SyncSettings, error) {
	st := SyncSettings{Lookback: "since_last"}
	err := s.db.QueryRowContext(ctx, `SELECT lookback, since_date FROM sync_settings WHERE user_id = ?`, userID).Scan(&st.Lookback, &st.SinceDate)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	return st, err
}

func (s *Store) PutSyncSettings(ctx context.Context, userID string, st SyncSettings) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sync_settings (user_id, lookback, since_date) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET lookback = excluded.lookback, since_date = excluded.since_date`, userID, st.Lookback, st.SinceDate)
	return err
}

// Maintain runs the free, idempotent repairs after a sync.
func (s *Store) Maintain(ctx context.Context) error {
	if _, err := s.SplitRolelessApplications(ctx); err != nil {
		return err
	}
	if _, err := s.MergeOrphanApplications(ctx); err != nil {
		return err
	}
	if err := s.LinkAllOrganizations(ctx); err != nil {
		return err
	}
	if err := s.SeedMailboxes(ctx); err != nil {
		return err
	}
	return s.RebuildAllHistory(ctx)
}
