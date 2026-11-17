package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/ingest/internal/classify"
	"github.com/ayush3923/project-dzinga/services/ingest/internal/secrets"
	"github.com/ayush3923/project-dzinga/services/ingest/internal/source"
)

const tok = "test-token-0123456789abcdef0123456789"

// fakeCore records what the sync service tells core-api.
type fakeCore struct {
	mu          sync.Mutex
	mailboxes   []mailbox
	settings    syncSettings
	states      map[int64][]map[string]any
	events      int
	maintenance int
	quizzes     int
	deleted     []string
}

func (f *fakeCore) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	auth := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+tok }
	mux.HandleFunc("GET /api/v1/mailboxes", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"mailboxes": f.mailboxes, "settings": f.settings})
	})
	mux.HandleFunc("PUT /internal/v1/mailboxes/{id}/state", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			w.WriteHeader(401)
			return
		}
		var st map[string]any
		json.NewDecoder(r.Body).Decode(&st)
		f.mu.Lock()
		var id int64
		for _, m := range f.mailboxes {
			if r.PathValue("id") == itoa(m.ID) {
				id = m.ID
			}
		}
		f.states[id] = append(f.states[id], st)
		f.mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /internal/v1/events", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Events []event }
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.events += len(body.Events)
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]int{"received": len(body.Events), "added": len(body.Events)})
	})
	mux.HandleFunc("POST /internal/v1/requests", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tok {
			w.WriteHeader(401)
			return
		}
		f.mu.Lock()
		f.quizzes++
		f.mu.Unlock()
		w.WriteHeader(202)
	})
	mux.HandleFunc("POST /internal/v1/maintenance", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.maintenance++
		f.mu.Unlock()
		w.WriteHeader(204)
	})
	mux.HandleFunc("DELETE /internal/v1/mailboxes/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.deleted = append(f.deleted, r.PathValue("id"))
		f.mu.Unlock()
		w.WriteHeader(204)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// fakeSource returns one job email per mailbox and records the start date it was given.
type fakeSource struct {
	m       mailbox
	secret  string
	sinces  *[]time.Time
	fail    error
	badPass bool
}

func (f *fakeSource) Fetch(_ context.Context, since time.Time, _ func(string, string) bool, fn func(source.Message) error) error {
	*f.sinces = append(*f.sinces, since)
	if f.fail != nil {
		return f.fail
	}
	return fn(source.Message{ID: "m-" + f.m.Address, Account: f.m.Address, Date: since.Add(time.Hour),
		Message: classify.Message{FromName: "Acme Careers", FromAddr: "jobs@acme.example", Subject: "Thank you for applying to Acme"}})
}

func (f *fakeSource) Verify() error {
	if f.badPass {
		return errors.New("the mail server rejected this app password")
	}
	return nil
}

func newTestSyncer(t *testing.T, core *fakeCore, failFor string) (*syncer, *httptest.Server, *[]time.Time) {
	srv := core.server(t)
	var sinces []time.Time
	s := &syncer{core: srv.URL, quiz: srv.URL, token: tok, clientID: "c", secrets: &secrets.Memory{}, http: srv.Client(),
		log: slog.New(slog.NewTextHandler(io.Discard, nil)), connects: map[int64]msConnect{},
		now: func() time.Time { return time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC) }}
	s.newSource = func(m mailbox, secret string) source.Source {
		fs := &fakeSource{m: m, secret: secret, sinces: &sinces, badPass: secret == "wrong"}
		if m.Address == failFor {
			fs.fail = source.ErrReconnect
		}
		return fs
	}
	api := httptest.NewServer(s.handler())
	t.Cleanup(api.Close)
	return s, api, &sinces
}

func call(t *testing.T, method, url, auth, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func waitIdle(t *testing.T, s *syncer) runState {
	t.Helper()
	for i := 0; i < 200; i++ {
		s.mu.Lock()
		st := s.state
		s.mu.Unlock()
		if !st.Running {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sync did not finish")
	return runState{}
}

func TestSyncRunsEnabledConnectedMailboxes(t *testing.T) {
	last := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	core := &fakeCore{states: map[int64][]map[string]any{}, settings: syncSettings{Lookback: "since_last"}, mailboxes: []mailbox{
		{ID: 1, Address: "me@yahoo.co.in", Provider: "yahoo", Enabled: true, CredentialSaved: true, LastSyncedAt: &last},
		{ID: 2, Address: "me@gmail.com", Provider: "gmail", Enabled: false, CredentialSaved: true},   // excluded by the user
		{ID: 3, Address: "me@live.in", Provider: "microsoft", Enabled: true, CredentialSaved: false}, // not connected
		{ID: 4, Address: "me@outlook.com", Provider: "microsoft", Enabled: true, CredentialSaved: true},
	}}
	s, api, sinces := newTestSyncer(t, core, "me@outlook.com")
	s.secrets.Set("me@yahoo.co.in", "apppass")
	s.secrets.Set("me@outlook.com", "rt-revoked")

	if res := call(t, "POST", api.URL+"/v1/sync", "Bearer "+tok, ""); res.StatusCode != 202 {
		t.Fatalf("start: %d", res.StatusCode)
	}
	st := waitIdle(t, s)
	if len(st.Mailboxes) != 2 || st.Mailboxes[0].Status != "done" || st.Mailboxes[0].Added != 1 || st.Mailboxes[1].Status != "error" {
		t.Fatalf("state: %+v", st)
	}
	// since_last overlaps 2 days with the last sync.
	if !(*sinces)[0].Equal(last.AddDate(0, 0, -2)) {
		t.Errorf("since = %v", (*sinces)[0])
	}
	// A revoked Microsoft sign-in is reported and marked for reconnection.
	got := core.states[4][0]
	if got["status"] != "error" || got["credential_saved"] != false {
		t.Errorf("outlook state: %+v", got)
	}
	if core.events != 1 || core.maintenance != 1 || core.quizzes != 1 {
		t.Errorf("events=%d maintenance=%d quiz requests=%d", core.events, core.maintenance, core.quizzes)
	}
}

func TestLookbackSettings(t *testing.T) {
	now := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	m := mailbox{}
	for lb, want := range map[string]time.Time{
		"14d": now.AddDate(0, 0, -14), "3m": now.AddDate(0, -3, 0), "12m": now.AddDate(-1, 0, 0),
		"since_last": now.AddDate(0, -2, 0), // never synced: 2 months
	} {
		if got := since(syncSettings{Lookback: lb}, m, now); !got.Equal(want) {
			t.Errorf("%s: %v", lb, got)
		}
	}
	if got := since(syncSettings{Lookback: "custom", SinceDate: "2026-01-01"}, m, now); got.Format("2006-01-02") != "2026-01-01" {
		t.Errorf("custom: %v", got)
	}
}

func TestOneSyncAtATime(t *testing.T) {
	core := &fakeCore{states: map[int64][]map[string]any{}}
	s, api, _ := newTestSyncer(t, core, "")
	s.mu.Lock()
	s.state.Running = true
	s.mu.Unlock()
	if res := call(t, "POST", api.URL+"/v1/sync", "Bearer "+tok, ""); res.StatusCode != 409 {
		t.Errorf("second sync: %d", res.StatusCode)
	}
}

func TestPasswordIsVerifiedThenKeptOutOfResponses(t *testing.T) {
	core := &fakeCore{states: map[int64][]map[string]any{}, mailboxes: []mailbox{
		{ID: 1, Address: "me@gmail.com", Provider: "gmail", Enabled: true},
		{ID: 2, Address: "me@live.in", Provider: "microsoft", Enabled: true},
	}}
	s, api, _ := newTestSyncer(t, core, "")
	res := call(t, "POST", api.URL+"/v1/mailboxes/1/password", "Bearer "+tok, `{"password":"wrong"}`)
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 400 || strings.Contains(string(b), "wrong\"") {
		t.Errorf("bad password: %d %s", res.StatusCode, b)
	}
	if _, err := s.secrets.Get("me@gmail.com"); err == nil {
		t.Error("a rejected password was saved")
	}
	if res := call(t, "POST", api.URL+"/v1/mailboxes/1/password", "Bearer "+tok, `{"password":"abcd efgh ijkl mnop"}`); res.StatusCode != 204 {
		t.Errorf("good password: %d", res.StatusCode)
	}
	if v, _ := s.secrets.Get("me@gmail.com"); v != "abcdefghijklmnop" {
		t.Errorf("saved %q (spaces should be stripped)", v)
	}
	if res := call(t, "POST", api.URL+"/v1/mailboxes/2/password", "Bearer "+tok, `{"password":"x"}`); res.StatusCode != 400 {
		t.Errorf("password for a Microsoft mailbox: %d", res.StatusCode)
	}
	if res := call(t, "POST", api.URL+"/v1/mailboxes/9/password", "Bearer "+tok, `{"password":"x"}`); res.StatusCode != 404 {
		t.Errorf("unknown mailbox: %d", res.StatusCode)
	}
	// Disconnect removes the credential; forget also removes the mailbox.
	if res := call(t, "DELETE", api.URL+"/v1/mailboxes/1?forget=true", "Bearer "+tok, ""); res.StatusCode != 204 {
		t.Errorf("forget: %d", res.StatusCode)
	}
	if _, err := s.secrets.Get("me@gmail.com"); err == nil || len(core.deleted) != 1 {
		t.Errorf("credential or mailbox not removed")
	}
}

func TestEveryRouteNeedsTheToken(t *testing.T) {
	core := &fakeCore{states: map[int64][]map[string]any{}}
	_, api, _ := newTestSyncer(t, core, "")
	for _, r := range [][2]string{{"GET", "/v1/sync"}, {"POST", "/v1/sync"}, {"POST", "/v1/mailboxes/1/password"},
		{"POST", "/v1/mailboxes/1/microsoft"}, {"DELETE", "/v1/mailboxes/1"}} {
		if res := call(t, r[0], api.URL+r[1], "Bearer nope", ""); res.StatusCode != 401 {
			t.Errorf("%s %s: %d", r[0], r[1], res.StatusCode)
		}
	}
}

// Before any sync, the status lists no mailboxes as [] (never null).
func TestStatusBeforeFirstSync(t *testing.T) {
	core := &fakeCore{states: map[int64][]map[string]any{}}
	s, api, _ := newTestSyncer(t, core, "")
	s.state = runState{Mailboxes: []progress{}}
	res := call(t, "GET", api.URL+"/v1/sync", "Bearer "+tok, "")
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), `"mailboxes":[]`) {
		t.Errorf("got %s", b)
	}
}

// Naming a mailbox runs it even when it isn't ticked for routine syncs.
func TestExplicitMailboxOverridesInclude(t *testing.T) {
	core := &fakeCore{states: map[int64][]map[string]any{}, settings: syncSettings{Lookback: "14d"}, mailboxes: []mailbox{
		{ID: 2, Address: "me@gmail.com", Provider: "gmail", Enabled: false, CredentialSaved: true},
	}}
	s, api, _ := newTestSyncer(t, core, "")
	s.secrets.Set("me@gmail.com", "apppass")
	if res := call(t, "POST", api.URL+"/v1/sync", "Bearer "+tok, `{"mailbox_ids":[2]}`); res.StatusCode != 202 {
		t.Fatalf("start: %d", res.StatusCode)
	}
	if st := waitIdle(t, s); len(st.Mailboxes) != 1 || st.Mailboxes[0].Status != "done" {
		t.Errorf("state: %+v", st)
	}
	s.mu.Lock()
	s.state = runState{Mailboxes: []progress{}}
	s.mu.Unlock()
	call(t, "POST", api.URL+"/v1/sync", "Bearer "+tok, "")
	if st := waitIdle(t, s); len(st.Mailboxes) != 0 {
		t.Errorf("unticked mailbox ran in a routine sync: %+v", st)
	}
}
