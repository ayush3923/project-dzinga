package main

// The sync service lets the web app connect mailboxes and run scans. It listens on
// loopback only, requires the internal token on every route, and keeps credentials
// in the Keychain: they are never stored in the database, logged or returned.

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayush3923/project-dzinga/services/ingest/internal/secrets"
	"github.com/ayush3923/project-dzinga/services/ingest/internal/source"
)

type mailbox struct {
	ID              int64      `json:"id"`
	Address         string     `json:"address"`
	Provider        string     `json:"provider"`
	Enabled         bool       `json:"enabled"`
	CredentialSaved bool       `json:"credential_saved"`
	LastSyncedAt    *time.Time `json:"last_synced_at"`
}

type syncSettings struct {
	Lookback  string `json:"lookback"`
	SinceDate string `json:"since_date"`
}

type progress struct {
	ID      int64  `json:"id"`
	Address string `json:"address"`
	Status  string `json:"status"` // pending | scanning | done | error
	Found   int    `json:"found"`
	Added   int    `json:"added"`
	Since   string `json:"since"`
	Error   string `json:"error,omitempty"`
}

type runState struct {
	Running    bool       `json:"running"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Mailboxes  []progress `json:"mailboxes"`
	Error      string     `json:"error,omitempty"`
}

type msConnect struct {
	Status string `json:"status"` // pending | connected | error
	Error  string `json:"error,omitempty"`
}

// imapServers maps providers to their IMAP host and the folders worth scanning.
var imapServers = map[string]struct {
	addr    string
	folders []string
}{
	"yahoo":  {"imap.mail.yahoo.com:993", []string{"INBOX", "Archive"}},
	"gmail":  {"imap.gmail.com:993", []string{"[Gmail]/All Mail"}},
	"icloud": {"imap.mail.me.com:993", []string{"INBOX", "Archive"}},
}

type syncer struct {
	core, token, clientID string
	agent                 string // agent base URL; "" = don't ask for a digest
	quiz                  string // quiz service base URL; "" = no quiz step
	orgs                  string // organizations service base URL; "" = no diagnosis step
	secrets               secrets.Store
	log                   *slog.Logger
	now                   func() time.Time
	http                  *http.Client
	// newSource builds the reader for a mailbox; tests replace it.
	newSource func(m mailbox, secret string) source.Source

	mu       sync.Mutex
	state    runState
	connects map[int64]msConnect
}

func (s *syncer) defaultSource(m mailbox, secret string) source.Source {
	if m.Provider == "microsoft" {
		return &source.Graph{ClientID: s.clientID, Account: m.Address, Folders: []string{"inbox", "archive"}, Log: s.log,
			RefreshToken: secret,
			OnRefresh: func(rt string) {
				if err := s.secrets.Set(m.Address, rt); err != nil {
					s.log.Warn("save_refresh_token_failed", "mailbox", m.ID)
				}
			}}
	}
	cfg := imapServers[m.Provider]
	return &source.IMAP{Addr: cfg.addr, User: m.Address, Password: secret, Folders: cfg.folders, Log: s.log}
}

// ---------- core-api calls ----------

func (s *syncer) coreDo(ctx context.Context, method, path string, in, out any) error {
	var body *bytes.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	} else {
		body = bytes.NewReader(nil)
	}
	req, _ := http.NewRequestWithContext(ctx, method, s.core+path, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.token)
	res, err := s.http.Do(req)
	if err != nil {
		return fmt.Errorf("core-api unreachable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if res.StatusCode >= 300 {
		return fmt.Errorf("core-api %s %s: %s", method, path, res.Status)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

var errNotFound = errors.New("mailbox not found")

func (s *syncer) mailboxes(ctx context.Context) ([]mailbox, syncSettings, error) {
	var out struct {
		Mailboxes []mailbox    `json:"mailboxes"`
		Settings  syncSettings `json:"settings"`
	}
	err := s.coreDo(ctx, "GET", "/api/v1/mailboxes", nil, &out)
	return out.Mailboxes, out.Settings, err
}

func (s *syncer) mailbox(ctx context.Context, id int64) (mailbox, error) {
	all, _, err := s.mailboxes(ctx)
	if err != nil {
		return mailbox{}, err
	}
	for _, m := range all {
		if m.ID == id {
			return m, nil
		}
	}
	return mailbox{}, errNotFound
}

func (s *syncer) setState(ctx context.Context, id int64, st map[string]any) {
	if err := s.coreDo(ctx, "PUT", fmt.Sprintf("/internal/v1/mailboxes/%d/state", id), st, nil); err != nil {
		s.log.Warn("mailbox_state_failed", "mailbox", id, "err", err)
	}
}

// since turns the user's lookback setting into a start date for one mailbox.
func since(set syncSettings, m mailbox, now time.Time) time.Time {
	switch set.Lookback {
	case "14d":
		return now.AddDate(0, 0, -14)
	case "1m":
		return now.AddDate(0, -1, 0)
	case "2m":
		return now.AddDate(0, -2, 0)
	case "3m":
		return now.AddDate(0, -3, 0)
	case "6m":
		return now.AddDate(0, -6, 0)
	case "12m":
		return now.AddDate(-1, 0, 0)
	case "custom":
		if t, err := time.Parse("2006-01-02", set.SinceDate); err == nil {
			return t
		}
	}
	// since_last: overlap 2 days with the last sync (re-reading is safe: no duplicates).
	if m.LastSyncedAt != nil {
		return m.LastSyncedAt.AddDate(0, 0, -2)
	}
	return now.AddDate(0, -2, 0)
}

// ---------- sync ----------

// start runs a sync of the given mailboxes (all ticked ones when empty). from, when
// set, is a one-off start date for this run (a backfill); the saved lookback is unchanged.
func (s *syncer) start(ids []int64, from *time.Time) (runState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Running {
		return s.state, errors.New("a sync is already running")
	}
	now := s.now()
	s.state = runState{Running: true, StartedAt: &now, Mailboxes: []progress{}}
	go s.run(ids, from)
	return s.state, nil
}

func (s *syncer) update(f func(*runState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(&s.state)
}

func (s *syncer) run(ids []int64, override *time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	defer s.update(func(st *runState) {
		now := s.now()
		st.Running, st.FinishedAt = false, &now
	})
	all, settings, err := s.mailboxes(ctx)
	if err != nil {
		s.update(func(st *runState) { st.Error = err.Error() })
		return
	}
	want := map[int64]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var queue []mailbox
	for _, m := range all {
		// Named mailboxes run even if unticked; otherwise only the ticked ones.
		if m.CredentialSaved && ((len(ids) == 0 && m.Enabled) || want[m.ID]) {
			queue = append(queue, m)
		}
	}
	startOf := func(m mailbox) time.Time {
		if override != nil {
			return *override
		}
		return since(settings, m, s.now())
	}
	s.update(func(st *runState) {
		for _, m := range queue {
			st.Mailboxes = append(st.Mailboxes, progress{ID: m.ID, Address: m.Address, Status: "pending",
				Since: startOf(m).Format("2006-01-02")})
		}
	})
	for i, m := range queue {
		set := func(f func(*progress)) { s.update(func(st *runState) { f(&st.Mailboxes[i]) }) }
		set(func(p *progress) { p.Status = "scanning" })
		from := startOf(m)
		secret, err := s.secrets.Get(m.Address)
		if err != nil {
			msg := "no saved credential: reconnect this mailbox"
			set(func(p *progress) { p.Status, p.Error = "error", msg })
			no := false
			s.setState(ctx, m.ID, map[string]any{"status": "error", "error": msg, "credential_saved": &no})
			continue
		}
		found, _, err := scan(ctx, s.newSource(m, secret), from)
		if err != nil {
			msg := err.Error()
			set(func(p *progress) { p.Status, p.Error = "error", msg })
			st := map[string]any{"status": "error", "error": msg}
			if errors.Is(err, source.ErrReconnect) {
				no := false
				st["credential_saved"] = &no
			}
			s.setState(ctx, m.ID, st)
			continue
		}
		added, err := send(ctx, s.core, s.token, found)
		if err != nil {
			msg := err.Error()
			set(func(p *progress) { p.Status, p.Error = "error", msg })
			s.setState(ctx, m.ID, map[string]any{"status": "error", "error": msg})
			continue
		}
		now := s.now()
		set(func(p *progress) { p.Status, p.Found, p.Added = "done", len(found), added })
		s.setState(ctx, m.ID, map[string]any{"status": "ok", "error": "", "synced_at": now, "added": added})
		s.log.Info("sync_mailbox", "mailbox", m.ID, "found", len(found), "added", added, "since", from.Format("2006-01-02"))
	}
	// Free, idempotent repairs: grouping, organizations, stage history.
	if err := s.coreDo(ctx, "POST", "/internal/v1/maintenance", nil, nil); err != nil {
		s.update(func(st *runState) { st.Error = "maintenance: " + err.Error() })
		return
	}
	// Ask the quiz service for today's quiz (Claude builds it on its next run). A
	// failure here doesn't fail the refresh.
	if s.quiz != "" {
		s.requestQuiz(ctx)
	}
	// Ask the organizations service to queue diagnoses for a few companies at random.
	if s.orgs != "" {
		s.requestDiagnoses(ctx)
	}
	// The morning digest for this refresh: one short model call, in the background
	// so the sync shows as done straight away. Without credit the template stays.
	if s.agent != "" {
		go s.requestDigest()
	}
}

func (s *syncer) requestQuiz(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.quiz+"/internal/v1/requests", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	res, err := s.http.Do(req)
	if err != nil {
		s.log.Warn("quiz_request_failed", "err", err)
		return
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		s.log.Warn("quiz_request_failed", "status", res.StatusCode)
		return
	}
	s.log.Info("quiz_requested")
}

func (s *syncer) requestDiagnoses(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.orgs+"/internal/v1/diagnosis-requests", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	res, err := s.http.Do(req)
	if err != nil {
		s.log.Warn("diagnosis_request_failed", "err", err)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		s.log.Warn("diagnosis_request_failed", "status", res.StatusCode)
		return
	}
	var body struct {
		Queued []json.RawMessage `json:"queued"`
	}
	json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body)
	s.log.Info("diagnoses_requested", "queued", len(body.Queued))
}

func (s *syncer) requestDigest() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.agent+"/v1/digest", nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	res, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		s.log.Warn("digest_request_failed", "err", err)
		return
	}
	res.Body.Close()
	s.log.Info("digest_requested", "status", res.StatusCode)
}

// ---------- connecting ----------

func (s *syncer) savePassword(ctx context.Context, id int64, password string) error {
	m, err := s.mailbox(ctx, id)
	if err != nil {
		return err
	}
	cfg, ok := imapServers[m.Provider]
	if !ok {
		return errors.New("this mailbox signs in with Microsoft, not an app password")
	}
	password = strings.Join(strings.Fields(password), "") // app passwords are shown in groups
	if password == "" || len(password) > 256 {
		return errors.New("enter the app password")
	}
	if v, ok := s.newSource(m, password).(interface{ Verify() error }); ok {
		if err := v.Verify(); err != nil {
			return err
		}
	} else if cfg.addr == "" {
		return errors.New("unknown mail server")
	}
	if err := s.secrets.Set(m.Address, password); err != nil {
		return err
	}
	yes := true
	s.setState(ctx, id, map[string]any{"credential_saved": &yes})
	return nil
}

func (s *syncer) startMicrosoft(ctx context.Context, id int64) (map[string]any, error) {
	m, err := s.mailbox(ctx, id)
	if err != nil {
		return nil, err
	}
	if m.Provider != "microsoft" {
		return nil, errors.New("this mailbox uses an app password")
	}
	if s.clientID == "" {
		return nil, errors.New("DZ_MS_CLIENT_ID is not set (see docs/microsoft-setup.md)")
	}
	var rt string
	g := &source.Graph{ClientID: s.clientID, Account: m.Address, Log: s.log, OnRefresh: func(t string) { rt = t }}
	d, err := g.StartDevice(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.connects[id] = msConnect{Status: "pending"}
	s.mu.Unlock()
	go func() {
		wctx, cancel := context.WithTimeout(context.Background(), d.ExpiresIn+time.Minute)
		defer cancel()
		result := msConnect{Status: "connected"}
		token, err := g.WaitDevice(wctx, d)
		if err == nil {
			err = g.Verify(wctx, token) // the right account signed in
		}
		if err == nil && rt == "" {
			err = errors.New("Microsoft did not return a renewable sign-in; check the app allows offline access")
		}
		if err == nil {
			err = s.secrets.Set(m.Address, rt)
		}
		if err != nil {
			result = msConnect{Status: "error", Error: err.Error()}
		} else {
			yes := true
			s.setState(wctx, id, map[string]any{"credential_saved": &yes})
		}
		s.mu.Lock()
		s.connects[id] = result
		s.mu.Unlock()
	}()
	return map[string]any{"verification_uri": d.VerificationURI, "user_code": d.UserCode, "expires_in": int(d.ExpiresIn.Seconds())}, nil
}

func (s *syncer) disconnect(ctx context.Context, id int64, forget bool) error {
	m, err := s.mailbox(ctx, id)
	if err != nil {
		return err
	}
	if err := s.secrets.Delete(m.Address); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	if forget {
		return s.coreDo(ctx, "DELETE", fmt.Sprintf("/internal/v1/mailboxes/%d", id), nil, nil)
	}
	no := false
	s.setState(ctx, id, map[string]any{"credential_saved": &no})
	return nil
}

// ---------- HTTP ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (s *syncer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
			h(w, r)
		}
	}
	id := func(r *http.Request) (int64, bool) {
		v, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		return v, err == nil && v > 0
	}
	fail := func(w http.ResponseWriter, err error) {
		status := http.StatusBadRequest
		if errors.Is(err, errNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
	}

	mux.HandleFunc("GET /v1/sync", auth(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		st := s.state
		st.Mailboxes = append(make([]progress, 0, len(s.state.Mailboxes)), s.state.Mailboxes...)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, st)
	}))
	mux.HandleFunc("POST /v1/sync", auth(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MailboxIDs []int64 `json:"mailbox_ids"`
			Since      string  `json:"since"` // optional one-off start date (YYYY-MM-DD), within the last year
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.MailboxIDs) > 20 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
				return
			}
		}
		var from *time.Time
		if body.Since != "" {
			t, err := time.Parse("2006-01-02", body.Since)
			if err != nil || t.After(s.now()) || t.Before(s.now().AddDate(-1, 0, 0)) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "since must be a date (YYYY-MM-DD) within the last year"})
				return
			}
			from = &t
		}
		st, err := s.start(body.MailboxIDs, from)
		if err != nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, st)
	}))
	mux.HandleFunc("POST /v1/mailboxes/{id}/password", auth(func(w http.ResponseWriter, r *http.Request) {
		mid, ok := id(r)
		var body struct {
			Password string `json:"password"`
		}
		if !ok || json.NewDecoder(r.Body).Decode(&body) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
			return
		}
		if err := s.savePassword(r.Context(), mid, body.Password); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.HandleFunc("POST /v1/mailboxes/{id}/microsoft", auth(func(w http.ResponseWriter, r *http.Request) {
		mid, ok := id(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		out, err := s.startMicrosoft(r.Context(), mid)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}))
	mux.HandleFunc("GET /v1/mailboxes/{id}/microsoft", auth(func(w http.ResponseWriter, r *http.Request) {
		mid, _ := id(r)
		s.mu.Lock()
		c, ok := s.connects[mid]
		s.mu.Unlock()
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no sign-in in progress"})
			return
		}
		writeJSON(w, http.StatusOK, c)
	}))
	mux.HandleFunc("DELETE /v1/mailboxes/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		mid, ok := id(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		if err := s.disconnect(r.Context(), mid, r.URL.Query().Get("forget") == "true"); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	return mux
}

func runServer(log *slog.Logger, core, clientID string) {
	token := os.Getenv("DZ_INTERNAL_TOKEN")
	if len(token) < 32 {
		fail(log, "DZ_INTERNAL_TOKEN must be set")
	}
	s := &syncer{core: core, token: token, clientID: clientID, log: log, now: time.Now, state: runState{Mailboxes: []progress{}},
		secrets: secrets.Keychain{Service: "dzinga-mail"}, http: &http.Client{Timeout: 30 * time.Second},
		connects: map[int64]msConnect{}}
	s.newSource = s.defaultSource
	s.agent = os.Getenv("DZ_AGENT_URL")
	if s.agent == "" {
		s.agent = "http://127.0.0.1:8082"
	}
	s.quiz = os.Getenv("DZ_QUIZ_URL")
	if s.quiz == "" {
		s.quiz = "http://127.0.0.1:8084"
	}
	s.orgs = os.Getenv("DZ_ORGS_URL")
	if s.orgs == "" {
		s.orgs = "http://127.0.0.1:8085"
	}
	addr := os.Getenv("DZ_INGEST_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8083" // loopback only
	}
	log.Info("sync service listening", "addr", addr)
	srv := &http.Server{Addr: addr, Handler: s.handler(), ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		fail(log, err.Error())
	}
}
