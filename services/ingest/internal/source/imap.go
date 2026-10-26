package source

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// IMAP reads mail over IMAPS. It opens folders with EXAMINE (read-only) and fetches
// bodies with BODY.PEEK, so messages are never marked as read.
type IMAP struct {
	Addr     string // e.g. imap.mail.yahoo.com:993
	User     string
	Password string // app password; held in memory only, never logged
	Folders  []string
	Log      *slog.Logger
}

const fetchChunk = 200

// Verify logs in and out, to check an app password before it is saved.
func (s *IMAP) Verify() error {
	host := strings.Split(s.Addr, ":")[0]
	c, err := client.DialTLS(s.Addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer c.Logout()
	c.Timeout = 30 * time.Second
	if err := c.Login(s.User, s.Password); err != nil {
		return errors.New("the mail server rejected this app password")
	}
	return nil
}

func (s *IMAP) Fetch(ctx context.Context, since time.Time, prefilter func(string, string) bool, fn func(Message) error) error {
	host := strings.Split(s.Addr, ":")[0]
	c, err := client.DialTLS(s.Addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer c.Logout()
	c.Timeout = 2 * time.Minute
	if err := c.Login(s.User, s.Password); err != nil {
		return fmt.Errorf("login failed (check the app password): %w", err)
	}

	folders, err := s.resolveFolders(c)
	if err != nil {
		return err
	}
	for _, folder := range folders {
		if err := s.fetchFolder(ctx, c, folder, since, prefilter, fn); err != nil {
			return fmt.Errorf("folder %q: %w", folder, err)
		}
	}
	return nil
}

// resolveFolders matches the requested names case-insensitively ("Inbox" vs "INBOX")
// and skips ones that don't exist.
func (s *IMAP) resolveFolders(c *client.Client) ([]string, error) {
	ch := make(chan *imap.MailboxInfo, 50)
	done := make(chan error, 1)
	go func() { done <- c.List("", "*", ch) }()
	var all []string
	for m := range ch {
		all = append(all, m.Name)
	}
	if err := <-done; err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	var out []string
	for _, want := range s.Folders {
		for _, have := range all {
			if strings.EqualFold(want, have) {
				out = append(out, have)
				break
			}
		}
	}
	s.Log.Info("imap_folders", "available", all, "scanning", out)
	return out, nil
}

// searchCap is the most UIDs some servers (Yahoo) return for one SEARCH.
const searchCap = 1000

// searchWindows searches in 30-day windows, halving any window that hits the
// server's result cap, so large mailboxes aren't silently truncated.
func (s *IMAP) searchWindows(c *client.Client, folder string, since time.Time) ([]uint32, error) {
	seen := map[uint32]bool{}
	var out []uint32
	var search func(from, to time.Time) error
	search = func(from, to time.Time) error {
		criteria := imap.NewSearchCriteria()
		criteria.Since, criteria.Before = from, to
		uids, err := c.UidSearch(criteria)
		if err != nil {
			return fmt.Errorf("search: %w", err)
		}
		if len(uids) >= searchCap && to.Sub(from) > 48*time.Hour {
			mid := from.Add(to.Sub(from) / 2).Truncate(24 * time.Hour)
			if err := search(from, mid); err != nil {
				return err
			}
			return search(mid, to)
		}
		if len(uids) >= searchCap {
			s.Log.Warn("imap_search_capped", "folder", folder, "day", from.Format("2006-01-02"))
		}
		for _, u := range uids {
			if !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
		return nil
	}
	end := time.Now().AddDate(0, 0, 1)
	for from := since; from.Before(end); from = from.AddDate(0, 0, 30) {
		to := from.AddDate(0, 0, 30)
		if to.After(end) {
			to = end
		}
		if err := search(from, to); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *IMAP) fetchFolder(ctx context.Context, c *client.Client, folder string, since time.Time,
	prefilter func(string, string) bool, fn func(Message) error) error {
	if _, err := c.Select(folder, true); err != nil { // true = read-only (EXAMINE)
		return err
	}
	uids, err := s.searchWindows(c, folder, since)
	if err != nil {
		return err
	}

	// Pass 1: envelopes only (cheap), to choose which bodies to download.
	var candidates []uint32
	for start := 0; start < len(uids); start += fetchChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		set := new(imap.SeqSet)
		set.AddNum(uids[start:min(start+fetchChunk, len(uids))]...)
		ch := make(chan *imap.Message, fetchChunk)
		done := make(chan error, 1)
		go func() { done <- c.UidFetch(set, []imap.FetchItem{imap.FetchEnvelope, imap.FetchUid}, ch) }()
		for msg := range ch {
			if msg.Envelope == nil || len(msg.Envelope.From) == 0 {
				continue
			}
			from := strings.ToLower(msg.Envelope.From[0].Address())
			if prefilter == nil || prefilter(from, decodeHeader(msg.Envelope.Subject)) {
				candidates = append(candidates, msg.Uid)
			}
		}
		if err := <-done; err != nil {
			return fmt.Errorf("fetch envelopes: %w", err)
		}
	}
	s.Log.Info("imap_scan", "folder", folder, "messages", len(uids), "candidates", len(candidates))

	// Pass 2: full bodies for candidates, peeking so \Seen is not set.
	section := &imap.BodySectionName{Peek: true}
	for start := 0; start < len(candidates); start += 50 {
		if err := ctx.Err(); err != nil {
			return err
		}
		set := new(imap.SeqSet)
		set.AddNum(candidates[start:min(start+50, len(candidates))]...)
		ch := make(chan *imap.Message, 50)
		done := make(chan error, 1)
		go func() { done <- c.UidFetch(set, []imap.FetchItem{section.FetchItem(), imap.FetchUid}, ch) }()
		var cbErr error
		for msg := range ch {
			if cbErr != nil {
				continue // drain
			}
			body := msg.GetBody(section)
			if body == nil {
				continue
			}
			m, err := Parse(body, s.User)
			if err != nil {
				s.Log.Warn("parse_failed", "folder", folder, "uid", msg.Uid)
				continue
			}
			cbErr = fn(m)
		}
		if err := <-done; err != nil {
			return fmt.Errorf("fetch bodies: %w", err)
		}
		if cbErr != nil {
			return cbErr
		}
	}
	return nil
}
