package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

const token = "test-token-0123456789abcdef0123456789"

var now = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

// newServer runs the real handler against a real SQLite file (integration test).
func newServer(t *testing.T, userID string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InternalToken: token, UserID: userID, Now: func() time.Time { return now }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func do(t *testing.T, method, url, auth string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if s, ok := body.(string); ok {
		r = strings.NewReader(s)
	} else if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
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

func ev(id, company, role, kind string, at time.Time) store.IngestEvent {
	return store.IngestEvent{MessageID: id, Account: "me@yahoo.com", OccurredAt: at,
		Sender: "jobs@" + strings.ToLower(company) + ".com", Subject: "About your application",
		Snippet: "snippet", Company: company, Role: role, Kind: kind}
}

func ingest(t *testing.T, ts *httptest.Server, events ...store.IngestEvent) {
	t.Helper()
	res := do(t, "POST", ts.URL+"/internal/v1/events", "Bearer "+token, map[string]any{"events": events})
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("ingest: %d %s", res.StatusCode, b)
	}
}

func decodeBody[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestIngestBuildsApplicationsAndStages(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(day int) time.Time { return time.Date(2026, 9, day, 10, 0, 0, 0, time.UTC) }
	ingest(t, ts,
		ev("m1", "Acme", "Backend Engineer", "applied", d(1)),
		ev("m2", "Acme", "Backend Engineer", "interview", d(5)),
		ev("m3", "Globex", "", "applied", d(2)),
		ev("m4", "Globex", "Product Manager", "rejected", d(9)), // role-less app gets upgraded
	)
	ingest(t, ts, ev("m2", "Acme", "Backend Engineer", "interview", d(5))) // duplicate: no-op

	res := do(t, "GET", ts.URL+"/api/v1/applications", "", nil)
	apps := decodeBody[struct{ Applications []store.Application }](t, res).Applications
	if len(apps) != 2 {
		t.Fatalf("want 2 applications, got %d: %+v", len(apps), apps)
	}
	got := map[string]store.Application{}
	for _, a := range apps {
		got[a.Company] = a
	}
	if a := got["Acme"]; a.Stage != "interview" || a.EventCount != 2 {
		t.Errorf("Acme: %+v", a)
	}
	if a := got["Globex"]; a.Stage != "rejected" || a.Role != "Product Manager" || a.EventCount != 2 {
		t.Errorf("Globex: %+v", a)
	}
	if a := got["Acme"]; len(a.Accounts) != 1 || a.Accounts[0] != "me@yahoo.com" {
		t.Errorf("Acme accounts: %+v", a.Accounts)
	}
}

func TestAccountsListEveryMailbox(t *testing.T) {
	ts, _ := newServer(t, "owner")
	y := ev("y1", "Amazon", "PM", "applied", now.AddDate(0, 0, -5))
	o := ev("o1", "Amazon", "PM", "rejected", now.AddDate(0, 0, -1))
	o.Account = "me@outlook.com"
	ingest(t, ts, y, o)
	apps := decodeBody[struct{ Applications []store.Application }](t,
		do(t, "GET", ts.URL+"/api/v1/applications", "", nil)).Applications
	if len(apps) != 1 || strings.Join(apps[0].Accounts, ",") != "me@outlook.com,me@yahoo.com" {
		t.Fatalf("got %+v", apps)
	}
}

func TestOutOfOrderEmailsUseLatestStage(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("late", "Initech", "SRE", "rejected", time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)))
	ingest(t, ts, ev("early", "Initech", "SRE", "applied", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)))
	apps := decodeBody[struct{ Applications []store.Application }](t,
		do(t, "GET", ts.URL+"/api/v1/applications", "", nil)).Applications
	if len(apps) != 1 || apps[0].Stage != "rejected" {
		t.Fatalf("got %+v", apps)
	}
	if !apps[0].AppliedAt.Equal(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("applied_at should be earliest email, got %v", apps[0].AppliedAt)
	}
}

func TestSearchAndStageFilter(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts,
		ev("a", "Acme", "Data Scientist", "applied", now.AddDate(0, 0, -3)),
		ev("b", "Globex", "Frontend Engineer", "interview", now.AddDate(0, 0, -2)))
	for _, tc := range []struct {
		query string
		want  int
	}{
		{"?q=data", 1}, {"?q=glob", 1}, {"?q=engineer", 1}, {"?stage=interview", 1}, {"?q=nothing", 0},
	} {
		apps := decodeBody[struct{ Applications []store.Application }](t,
			do(t, "GET", ts.URL+"/api/v1/applications"+tc.query, "", nil)).Applications
		if len(apps) != tc.want {
			t.Errorf("%s: want %d, got %d", tc.query, tc.want, len(apps))
		}
	}
}

func TestSummaryFlagsQuietApplications(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts,
		ev("old", "Acme", "SRE", "applied", now.AddDate(0, 0, -30)),
		ev("new", "Globex", "SRE", "applied", now.AddDate(0, 0, -2)),
		ev("rej", "Initech", "SRE", "rejected", now.AddDate(0, 0, -40)))
	sum := decodeBody[store.Summary](t, do(t, "GET", ts.URL+"/api/v1/summary", "", nil))
	if sum.Total != 3 || sum.ByStage["applied"] != 2 {
		t.Errorf("summary counts: %+v", sum)
	}
	if len(sum.NeedsNudge) != 1 || sum.NeedsNudge[0].Company != "Acme" {
		t.Errorf("needs_nudge: %+v", sum.NeedsNudge)
	}
}

func TestManualStageOverride(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a", "Acme", "SRE", "applied", now))
	res := do(t, "PATCH", ts.URL+"/api/v1/applications/1", "", map[string]string{"stage": "offer"})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("patch: %d", res.StatusCode)
	}
	app := decodeBody[struct{ Application store.Application }](t,
		do(t, "GET", ts.URL+"/api/v1/applications/1", "", nil)).Application
	if app.Stage != "offer" || !app.StageIsManual {
		t.Errorf("got %+v", app)
	}
	if res := do(t, "PATCH", ts.URL+"/api/v1/applications/1", "", map[string]string{"stage": "hired!"}); res.StatusCode != 400 {
		t.Errorf("invalid stage accepted: %d", res.StatusCode)
	}
}

func TestIntentRoundTripAndValidation(t *testing.T) {
	ts, _ := newServer(t, "owner")
	in := store.Intent{Roles: []string{"Product Manager"}, Locations: []string{"Bengaluru"},
		SalaryMin: 3000000, SalaryMax: 4500000, Currency: "INR", Situation: "growth"}
	if res := do(t, "PUT", ts.URL+"/api/v1/intent", "", in); res.StatusCode != 200 {
		t.Fatalf("put: %d", res.StatusCode)
	}
	got := decodeBody[store.Intent](t, do(t, "GET", ts.URL+"/api/v1/intent", "", nil))
	if got.Situation != "growth" || got.SalaryMax != 4500000 {
		t.Errorf("got %+v", got)
	}
	huge := in
	huge.SalaryMin, huge.SalaryMax = 600_000_000_000, 850_000_000_000 // rupees typed into an LPA field
	if res := do(t, "PUT", ts.URL+"/api/v1/intent", "", huge); res.StatusCode != 400 {
		t.Errorf("implausible salary accepted: %d", res.StatusCode)
	}
	bad := in
	bad.SalaryMin, bad.SalaryMax = 10, 5
	if res := do(t, "PUT", ts.URL+"/api/v1/intent", "", bad); res.StatusCode != 400 {
		t.Errorf("inverted salary range accepted: %d", res.StatusCode)
	}
}

// ---- Security tests (Rule 1) ----

func TestInternalEndpointRequiresToken(t *testing.T) {
	ts, _ := newServer(t, "owner")
	body := map[string]any{"events": []store.IngestEvent{ev("x", "Acme", "", "applied", now)}}
	for _, auth := range []string{"", "Bearer wrong", "Bearer " + token + "x", token} {
		if res := do(t, "POST", ts.URL+"/internal/v1/events", auth, body); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: want 401, got %d", auth, res.StatusCode)
		}
	}
}

func TestSearchIsInjectionSafe(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a", "Acme", "SRE", "applied", now))
	for _, q := range []string{`'; DROP TABLE applications; --`, `" OR 1=1`, `acme NEAR(x)`, `*`, `role:*`, `"unterminated`} {
		res := do(t, "GET", ts.URL+"/api/v1/applications?q="+urlEscape(q), "", nil)
		if res.StatusCode != 200 {
			t.Errorf("q=%q: status %d", q, res.StatusCode)
		}
	}
	apps := decodeBody[struct{ Applications []store.Application }](t,
		do(t, "GET", ts.URL+"/api/v1/applications", "", nil)).Applications
	if len(apps) != 1 {
		t.Fatalf("table damaged: %d applications", len(apps))
	}
}

func TestDataIsScopedToUser(t *testing.T) {
	// Two servers share one database but act as different users.
	tsA, st := newServer(t, "alice")
	ingest(t, tsA, ev("a", "Acme", "SRE", "applied", now))
	b := &Server{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InternalToken: token, UserID: "bob", Now: func() time.Time { return now }}
	tsB := httptest.NewServer(b.Handler())
	defer tsB.Close()

	if res := do(t, "GET", tsB.URL+"/api/v1/applications/1", "", nil); res.StatusCode != 404 {
		t.Errorf("bob read alice's application: %d", res.StatusCode)
	}
	if res := do(t, "PATCH", tsB.URL+"/api/v1/applications/1", "", map[string]string{"stage": "offer"}); res.StatusCode != 404 {
		t.Errorf("bob modified alice's application: %d", res.StatusCode)
	}
	apps := decodeBody[struct{ Applications []store.Application }](t,
		do(t, "GET", tsB.URL+"/api/v1/applications?q=acme", "", nil)).Applications
	if len(apps) != 0 {
		t.Errorf("bob's search leaked alice's data: %+v", apps)
	}
}

func TestRejectsOversizedAndMalformedBodies(t *testing.T) {
	ts, _ := newServer(t, "owner")
	huge := `{"roles":["` + strings.Repeat("a", maxBody) + `"]}`
	if res := do(t, "PUT", ts.URL+"/api/v1/intent", "", huge); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/api/v1/intent", "", `{"roles":[],"is_admin":true}`); res.StatusCode != 400 {
		t.Errorf("unknown field accepted: %d", res.StatusCode)
	}
	if res := do(t, "GET", ts.URL+"/api/v1/applications/abc", "", nil); res.StatusCode != 400 {
		t.Errorf("non-numeric id: %d", res.StatusCode)
	}
}

func TestErrorsDoNotLeakInternals(t *testing.T) {
	ts, st := newServer(t, "owner")
	st.Close() // force every query to fail
	res := do(t, "GET", ts.URL+"/api/v1/summary", "", nil)
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 500 || strings.Contains(strings.ToLower(string(b)), "sql") {
		t.Errorf("got %d %s", res.StatusCode, b)
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts, _ := newServer(t, "owner")
	res := do(t, "GET", ts.URL+"/api/v1/summary", "", nil)
	for h, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store"} {
		if got := res.Header.Get(h); got != want {
			t.Errorf("%s = %q", h, got)
		}
	}
	if res.Header.Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id")
	}
}

func urlEscape(s string) string {
	r := strings.NewReplacer("%", "%25", " ", "%20", "'", "%27", `"`, "%22", ";", "%3B", "=", "%3D",
		"(", "%28", ")", "%29", "*", "%2A", ":", "%3A", "-", "%2D")
	return r.Replace(s)
}
