// ingest reads a mailbox, keeps only emails about job applications, and sends them
// to core-api. Run it yourself; it asks for the mailbox app password with no echo.
//
//	go run . --user you@yahoo.com
//	go run . --user you@yahoo.com --dry-run      # preview, sends nothing
//	go run . --source graph --user you@live.in    # Microsoft account, signs in via browser
//	go run . --source dir --dir ./testdata/demo   # offline demo
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/ayush3923/project-dzinga/services/ingest/internal/classify"
	"github.com/ayush3923/project-dzinga/services/ingest/internal/source"
)

type event struct {
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
	JobURL     string    `json:"job_url,omitempty"`
	JobRef     string    `json:"job_ref,omitempty"` // requisition id, when the email gives one
}

func main() {
	var (
		src      = flag.String("source", "imap", "imap | graph | dir")
		user     = flag.String("user", "", "mailbox address, e.g. you@yahoo.com")
		addr     = flag.String("imap-addr", "imap.mail.yahoo.com:993", "IMAP server host:port")
		folders  = flag.String("folders", "INBOX,Archive", "comma-separated folders to scan")
		clientID = flag.String("client-id", os.Getenv("DZ_MS_CLIENT_ID"), "Microsoft app registration client ID (with --source graph)")
		sinceStr = flag.String("since", time.Now().AddDate(0, -18, 0).Format("2006-01-02"), "only mail received on/after this date")
		dir      = flag.String("dir", "", "folder of .eml files (with --source dir)")
		coreURL  = flag.String("core-url", "http://127.0.0.1:8081", "core-api base URL")
		dryRun   = flag.Bool("dry-run", false, "print what was found; send nothing")
		serve    = flag.Bool("serve", false, "run the sync service for the web app (127.0.0.1:8083)")
	)
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "ingest")
	if *serve {
		runServer(log, *coreURL, *clientID)
		return
	}

	since, err := time.Parse("2006-01-02", *sinceStr)
	if err != nil {
		fail(log, "invalid --since (use YYYY-MM-DD)")
	}
	token := os.Getenv("DZ_INTERNAL_TOKEN")
	if !*dryRun && token == "" {
		fail(log, "DZ_INTERNAL_TOKEN is not set (needed to send results to core-api)")
	}

	var s source.Source
	account := *user
	switch *src {
	case "imap":
		if *user == "" {
			fail(log, "--user is required")
		}
		s = &source.IMAP{Addr: *addr, User: *user, Password: readPassword(log),
			Folders: strings.Split(*folders, ","), Log: log}
	case "graph":
		if *user == "" || *clientID == "" {
			fail(log, "--user and --client-id (or DZ_MS_CLIENT_ID) are required with --source graph")
		}
		s = &source.Graph{ClientID: *clientID, Account: *user, Folders: strings.Split(*folders, ","), Log: log,
			Prompt: func(uri, code string) {
				fmt.Fprintf(os.Stderr, "\nTo allow read-only access to %s:\n  1. Open %s\n  2. Enter the code %s\n  3. Sign in and approve\nWaiting…\n\n", *user, uri, code)
			}}
	case "dir":
		if *dir == "" {
			fail(log, "--dir is required with --source dir")
		}
		if account == "" {
			account = "demo@example.com"
		}
		s = &source.Dir{Path: *dir, Account: account}
	default:
		fail(log, "unknown --source")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	start := time.Now()
	found, scanned, err := scan(ctx, s, since)
	if err != nil {
		fail(log, err.Error())
	}
	log.Info("scan_complete", "candidates_downloaded", scanned, "job_emails", len(found),
		"duration_ms", time.Since(start).Milliseconds())

	if *dryRun {
		printTable(found)
		return
	}
	if _, err := send(ctx, *coreURL, token, found); err != nil {
		fail(log, err.Error())
	}
	fmt.Fprintf(os.Stderr, "\nDone: %d job emails sent to core-api. Open the web app to see them.\n", len(found))
}

// scan reads a mailbox and keeps the job emails, oldest first (a rejection must
// find the application it answers).
func scan(ctx context.Context, s source.Source, since time.Time) ([]event, int, error) {
	var found []event
	scanned := 0
	err := s.Fetch(ctx, since, classify.LooksRelevant, func(m source.Message) error {
		scanned++
		if ownMessage(m) {
			return nil // something the user sent (Gmail's All Mail includes Sent)
		}
		r, ok := classify.Classify(m.Message)
		if !ok {
			return nil
		}
		found = append(found, event{
			MessageID: m.ID, Account: m.Account, OccurredAt: m.Date, Sender: m.FromAddr,
			SenderName: truncate(m.FromName, 200), JobURL: truncate(classify.JobURL(m.Links), 2000),
			Subject: truncate(m.Subject, 500), Snippet: classify.Snippet(m.Text, 400),
			Company: truncate(r.Company, 200), Role: truncate(r.Role, 200), Kind: r.Kind,
			JobRef: truncate(classify.JobRef(m.Subject, m.Text), 40),
		})
		return nil
	})
	sort.SliceStable(found, func(i, j int) bool { return found[i].OccurredAt.Before(found[j].OccurredAt) })
	return found, scanned, err
}

// ownMessage is true for mail the user sent from this mailbox.
func ownMessage(m source.Message) bool {
	return m.Account != "" && strings.EqualFold(strings.TrimSpace(m.FromAddr), strings.TrimSpace(m.Account))
}

// readPassword takes the app password from DZ_IMAP_PASSWORD or an echo-free prompt.
// It is deliberately not a flag, so it never lands in shell history or `ps` output.

func readPassword(log *slog.Logger) string {
	if p := os.Getenv("DZ_IMAP_PASSWORD"); p != "" {
		return p
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fail(log, "no terminal to prompt for the password; set DZ_IMAP_PASSWORD")
	}
	fmt.Fprint(os.Stderr, "Mailbox app password (input hidden): ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	// App passwords never contain whitespace, but they're often displayed in groups
	// ("abcd efgh ...") and pasted with spaces, so strip them all.
	pw := strings.Join(strings.Fields(string(b)), "")
	if err != nil || pw == "" {
		fail(log, "no password entered")
	}
	return pw
}

// send posts events to core-api in batches and returns how many were new.
func send(ctx context.Context, base, token string, events []event) (int, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	added := 0
	for i := 0; i < len(events); i += 200 {
		body, _ := json.Marshal(map[string]any{"events": events[i:min(i+200, len(events))]})
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/internal/v1/events", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			return added, fmt.Errorf("send to core-api (is it running?): %w", err)
		}
		var out struct {
			Added int `json:"added"`
		}
		json.NewDecoder(res.Body).Decode(&out)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return added, fmt.Errorf("core-api returned %s", res.Status)
		}
		added += out.Added
	}
	return added, nil
}

func printTable(events []event) {
	sort.Slice(events, func(i, j int) bool { return events[i].OccurredAt.After(events[j].OccurredAt) })
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "DATE\tSTAGE\tCOMPANY\tROLE\tREQ ID\tFROM\tSUBJECT")
	for _, e := range events {
		domain := e.Sender[strings.LastIndex(e.Sender, "@")+1:]
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.OccurredAt.Format("2006-01-02"), e.Kind,
			truncate(e.Company, 28), truncate(e.Role, 34), e.JobRef, truncate(domain, 24), truncate(e.Subject, 50))
	}
	w.Flush()
	fmt.Printf("\n%d job emails found (dry run: nothing was sent).\n", len(events))
}

// truncate cuts to at most n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func fail(log *slog.Logger, msg string) {
	log.Error(msg)
	os.Exit(1)
}
