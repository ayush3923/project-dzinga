package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/startoff/internal/jobs"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/peers"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/store"
)

const token = "test-token-0123456789abcdef0123456789"

var ist = time.FixedZone("IST", 5*3600+1800)

// fakePeers stands in for the organizations, sync and quiz services.
type fakePeers struct {
	orgs       []store.Candidate
	syncErr    error
	mailboxes  []peers.Mailbox
	syncs      int
	quizzes    int
	stillBusy  int // State reports running this many times
	candidates error
}

func (f *fakePeers) Org(_ context.Context, id int64) (store.Org, error) {
	for _, o := range f.orgs {
		if o.ID == id {
			return o.Org, nil
		}
	}
	return store.Org{}, peers.ErrNotFound
}

func (f *fakePeers) Candidates(context.Context) ([]store.Candidate, error) {
	return f.orgs, f.candidates
}

func (f *fakePeers) Start(context.Context) error { f.syncs++; return f.syncErr }

func (f *fakePeers) State(context.Context) (peers.SyncState, error) {
	if f.stillBusy > 0 {
		f.stillBusy--
		return peers.SyncState{Running: true}, nil
	}
	return peers.SyncState{Mailboxes: f.mailboxes}, nil
}

func (f *fakePeers) RequestToday(context.Context) error { f.quizzes++; return nil }

type env struct {
	ts    *httptest.Server
	st    *store.Store
	p     *fakePeers
	now   *time.Time
	runer *jobs.Runner
}

func newEnv(t *testing.T, orgs int) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "startoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := &fakePeers{mailboxes: []peers.Mailbox{{Address: "me@yahoo.com", Status: "done", Added: 2}}}
	for i := 1; i <= orgs; i++ {
		p.orgs = append(p.orgs, store.Candidate{Org: store.Org{ID: int64(i), Name: "Company " + strconv.Itoa(i)}, Applications: 1})
	}
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, ist)
	e := &env{st: st, p: p, now: &now}
	clock := func() time.Time { return *e.now }
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	e.runer = &jobs.Runner{Store: st, Orgs: p, Ingest: p, Quiz: p, Log: log, UserID: "owner", Now: clock, Loc: ist,
		At: 7 * time.Hour, Poll: time.Millisecond, SyncTimeout: time.Minute, Rand: rand.New(rand.NewPCG(1, 2))}
	s := &Server{Store: st, Orgs: p, Runner: e.runer, Log: log, InternalToken: token, UserID: "owner", Now: clock,
		Background: func(f func()) { f() }}
	e.ts = httptest.NewServer(s.Handler())
	t.Cleanup(e.ts.Close)
	return e
}

func (e *env) do(t *testing.T, method, path string, internal bool, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, r)
	if internal {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func decodeBody[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	defer res.Body.Close()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatalf("decode (%d): %v", res.StatusCode, err)
	}
	return v
}

func (e *env) later(d time.Duration) { *e.now = e.now.Add(d) }

func TestResearchKeepsHistory(t *testing.T) {
	e := newEnv(t, 2)
	profile := func(overview string) store.CompanyProfile {
		return store.CompanyProfile{Overview: overview, InterviewAngle: "Lead with growth.", Model: "claude-opus-5-5",
			News:    []store.NewsItem{{Title: "Acme raises", PublishedOn: "2026-09-01", URL: "https://news.example/acme"}},
			Leaders: []store.Leader{{Name: "Wile E.", Title: "CEO"}}, Ratings: []store.Rating{{Source: "glassdoor", Scale: 5}}}
	}
	if res := e.do(t, "POST", "/api/v1/organizations/1/research", false, nil); res.StatusCode != 202 {
		t.Fatalf("request: %d", res.StatusCode)
	}
	for _, ov := range []string{"Acme makes rockets.", "Acme makes rockets and anvils."} {
		if res := e.do(t, "PUT", "/internal/v1/organizations/1/profile", true, profile(ov)); res.StatusCode != 204 {
			t.Fatalf("profile: %d", res.StatusCode)
		}
		e.later(time.Hour)
	}
	brief := store.Briefing{CEO: "Wile E. Coyote", Model: "claude-opus-5-5",
		RevenueYear: store.Figure{Value: "$1.2B", Period: "FY2025", URL: "https://ir.acme.example/fy25"},
		News:        []store.BriefingNews{{Day: "2026-11-20", Title: "Acme buys back stock", URL: "https://news.example/a"}}}
	for range 2 { // same facts and link twice: one facts row, one news item
		if res := e.do(t, "PUT", "/internal/v1/organizations/1/briefing", true, brief); res.StatusCode != 204 {
			t.Fatalf("briefing: %d", res.StatusCode)
		}
		e.later(time.Hour)
	}
	brief.CEO, brief.News = "Road Runner", []store.BriefingNews{{Day: "2026-11-21", Title: "Acme names a new CEO", URL: "https://news.example/b"}}
	if res := e.do(t, "PUT", "/internal/v1/organizations/1/briefing", true, brief); res.StatusCode != 204 {
		t.Fatalf("briefing: %d", res.StatusCode)
	}
	for _, s := range []string{"Silent so far.", "Shortlisted now; keep going."} {
		d := store.Diagnosis{Summary: s, Model: "claude-opus-5-5", Causes: []store.DiagnosisItem{{Text: "Timing", Confidence: "medium"}}, Actions: []string{"Wait"}}
		if res := e.do(t, "POST", "/internal/v1/organizations/1/diagnosis", true, d); res.StatusCode != 204 {
			t.Fatalf("diagnosis: %d", res.StatusCode)
		}
		e.later(time.Hour)
	}

	r := decodeBody[store.Research](t, e.do(t, "GET", "/api/v1/organizations/1", false, nil))
	switch {
	case r.Profile == nil || r.Profile.Overview != "Acme makes rockets and anvils." || r.ProfileVersions != 2 || len(r.Profile.News) != 1:
		t.Errorf("profile: %+v versions=%d", r.Profile, r.ProfileVersions)
	case r.Briefing == nil || r.Briefing.CEO != "Road Runner" || len(r.Briefing.News) != 2 || r.NewsCount != 2:
		t.Errorf("briefing: %+v", r.Briefing)
	case r.Diagnosis == nil || r.Diagnosis.Summary != "Shortlisted now; keep going." || len(r.EarlierDiagnoses) != 1:
		t.Errorf("diagnosis: %+v earlier=%d", r.Diagnosis, len(r.EarlierDiagnoses))
	case r.ResearchRequestedAt != nil:
		t.Error("saving the profile didn't complete the request")
	case !r.BriefingUnseen || !r.DiagnosisUnseen || !r.HasProfile || !r.HasDiagnosis:
		t.Errorf("flags: %+v", r.Flags)
	}
	h := decodeBody[store.History](t, e.do(t, "GET", "/api/v1/organizations/1/history", false, nil))
	if len(h.Profiles) != 2 || len(h.Facts) != 2 || len(h.News) != 2 || len(h.Diagnoses) != 2 || h.Facts[1].CEO != "Wile E. Coyote" {
		t.Errorf("history: %d profiles, %d facts, %d news, %d diagnoses", len(h.Profiles), len(h.Facts), len(h.News), len(h.Diagnoses))
	}

	if res := e.do(t, "POST", "/api/v1/organizations/1/seen", false, nil); res.StatusCode != 204 {
		t.Fatalf("seen: %d", res.StatusCode)
	}
	flags := decodeBody[struct{ Flags []store.Flags }](t, e.do(t, "GET", "/api/v1/organizations/flags", false, nil)).Flags
	if len(flags) != 1 || flags[0].BriefingUnseen || flags[0].DiagnosisUnseen || !flags[0].HasDiagnosis {
		t.Errorf("flags after opening: %+v", flags)
	}
	// An organization with no research reads as empty, not missing.
	if r := decodeBody[store.Research](t, e.do(t, "GET", "/api/v1/organizations/2", false, nil)); r.Profile != nil || r.HasDiagnosis {
		t.Errorf("empty research: %+v", r)
	}
}

func TestResearchValidatesAndChecksTheOrganization(t *testing.T) {
	e := newEnv(t, 1)
	good := store.Diagnosis{Summary: "Too early.", Model: "m", Causes: []store.DiagnosisItem{}, Actions: []string{}}
	if res := e.do(t, "POST", "/internal/v1/organizations/1/diagnosis", false, good); res.StatusCode != 401 {
		t.Errorf("without token: %d", res.StatusCode)
	}
	if res := e.do(t, "POST", "/internal/v1/organizations/99/diagnosis", true, good); res.StatusCode != 404 {
		t.Errorf("unknown organization: %d", res.StatusCode)
	}
	bad := store.Briefing{Model: "m", News: []store.BriefingNews{{Day: "2026-11-20", Title: "Bad link here", URL: "javascript:alert(1)"}}}
	if res := e.do(t, "PUT", "/internal/v1/organizations/1/briefing", true, bad); res.StatusCode != 400 {
		t.Errorf("javascript link accepted: %d", res.StatusCode)
	}
	if res := e.do(t, "POST", "/api/v1/organizations/99/research", false, nil); res.StatusCode != 404 {
		t.Errorf("research for an unknown organization: %d", res.StatusCode)
	}
}

func TestDailyRunRefreshesRequestsQuizAndQueuesDiagnoses(t *testing.T) {
	e := newEnv(t, 8)
	e.p.stillBusy = 2 // the refresh takes a couple of polls
	// (the API starts the refresh before answering and the run joins it: two starts)
	res := e.do(t, "POST", "/api/v1/runs", false, nil)
	if res.StatusCode != 202 {
		t.Fatalf("start: %d", res.StatusCode)
	}
	runs := decodeBody[struct{ Runs []store.Run }](t, e.do(t, "GET", "/api/v1/runs", false, nil)).Runs
	if len(runs) != 1 {
		t.Fatalf("runs: %+v", runs)
	}
	r := runs[0]
	if r.Status != "done" || r.Trigger != "manual" || r.Day != "2026-11-20" || len(r.Steps) != 3 || e.p.syncs != 2 || e.p.quizzes != 1 {
		t.Fatalf("run: %+v syncs=%d quizzes=%d", r, e.p.syncs, e.p.quizzes)
	}
	if r.Steps[0].Detail != "1 of 1 mailboxes refreshed, 2 new emails" {
		t.Errorf("refresh detail: %q", r.Steps[0].Detail)
	}
	pending := decodeBody[struct{ Requests []store.ResearchRequest }](t, e.do(t, "GET", "/internal/v1/research-requests", true, nil)).Requests
	if len(pending) != store.DiagnosesPerRun {
		t.Fatalf("queued %d diagnoses", len(pending))
	}

	// Another run before Claude works the queue adds nothing; a failing mailbox makes it partial.
	e.p.mailboxes = append(e.p.mailboxes, peers.Mailbox{Address: "me@gmail.com", Status: "error", Error: "too many connections"})
	e.later(time.Hour)
	e.do(t, "POST", "/internal/v1/runs", true, nil).Body.Close()
	runs = decodeBody[struct{ Runs []store.Run }](t, e.do(t, "GET", "/api/v1/runs", false, nil)).Runs
	if r := runs[0]; r.Trigger != "claude" || r.Status != "partial" || r.Steps[0].Status != "failed" || r.Steps[1].Status != "done" {
		t.Errorf("second run: %+v", r)
	}
	if n := len(decodeBody[struct{ Requests []store.ResearchRequest }](t, e.do(t, "GET", "/internal/v1/research-requests", true, nil)).Requests); n != 5 {
		t.Errorf("second run grew the queue to %d", n)
	}
	if res := e.do(t, "POST", "/internal/v1/runs", false, nil); res.StatusCode != 401 {
		t.Errorf("Claude's run without token: %d", res.StatusCode)
	}
}

func TestDiagnosesSkipFreshOnesAndRefreshStale(t *testing.T) {
	e := newEnv(t, 1)
	ctx := context.Background()
	org := e.p.orgs[0]
	d := store.Diagnosis{Summary: "Too early.", Model: "m"}
	if err := e.st.SaveDiagnosis(ctx, "owner", org.Org, d, *e.now); err != nil {
		t.Fatal(err)
	}
	q, err := e.st.QueueDiagnoses(ctx, "owner", e.p.orgs, 5, 0, *e.now, nil)
	if err != nil || len(q) != 0 {
		t.Fatalf("fresh diagnosis queued again: %v %+v", err, q)
	}
	activity := e.now.Add(time.Hour)
	e.p.orgs[0].LastActivityAt = &activity
	if q, _ := e.st.QueueDiagnoses(ctx, "owner", e.p.orgs, 5, 0, e.now.Add(2*time.Hour), nil); len(q) != 1 {
		t.Errorf("activity since the diagnosis didn't queue it: %+v", q)
	}
}

func TestScheduleIsDueOnceAfterSevenLocal(t *testing.T) {
	e := newEnv(t, 1)
	ctx := context.Background()
	*e.now = time.Date(2026, 11, 20, 6, 59, 0, 0, ist)
	if due, _ := e.runer.Due(ctx); due {
		t.Fatal("due before 07:00")
	}
	*e.now = time.Date(2026, 11, 20, 9, 30, 0, 0, ist) // the Mac was asleep at 07:00
	if due, _ := e.runer.Due(ctx); !due {
		t.Fatal("not due after 07:00 with no run")
	}
	run, err := e.runer.Begin(ctx, store.TriggerTimer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.runer.Begin(ctx, store.TriggerManual); !errors.Is(err, store.ErrRunning) {
		t.Errorf("second run while one is running: %v", err)
	}
	e.runer.Execute(ctx, run)
	if due, _ := e.runer.Due(ctx); due {
		t.Error("still due after today's run")
	}
	*e.now = time.Date(2026, 11, 21, 7, 0, 0, 0, ist)
	if due, _ := e.runer.Due(ctx); !due {
		t.Error("not due the next morning")
	}
}

func TestInterruptedRunIsClosed(t *testing.T) {
	e := newEnv(t, 1)
	ctx := context.Background()
	if _, err := e.runer.Begin(ctx, store.TriggerTimer); err != nil {
		t.Fatal(err)
	}
	if err := e.st.CloseInterrupted(ctx, *e.now); err != nil {
		t.Fatal(err)
	}
	runs, _ := e.st.Runs(ctx, "owner", 5)
	if runs[0].Status != "partial" || runs[0].Steps[0].Name != "interrupted" {
		t.Errorf("interrupted run: %+v", runs[0])
	}
	if _, err := e.runer.Begin(ctx, store.TriggerManual); err != nil {
		t.Errorf("a new run after an interrupted one: %v", err)
	}
}
