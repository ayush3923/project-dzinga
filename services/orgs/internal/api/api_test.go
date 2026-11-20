package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/orgs/internal/store"
)

const token = "test-token-0123456789abcdef0123456789"

var now = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

// fakeCore stands in for core-api's analysis.
type fakeCore struct {
	apps []json.RawMessage
	err  error
}

func (f *fakeCore) Applications(context.Context) ([]json.RawMessage, error) { return f.apps, f.err }

type step struct {
	stage   string
	daysAgo int
}

// app builds an application as core-api's analysis reports it.
func app(id int64, company, role string, steps ...step) json.RawMessage {
	type ts struct {
		Stage string    `json:"stage"`
		Label string    `json:"label"`
		At    time.Time `json:"at"`
	}
	var tl []ts
	for _, s := range steps {
		tl = append(tl, ts{Stage: s.stage, Label: s.stage, At: now.AddDate(0, 0, -s.daysAgo)})
	}
	last := steps[len(steps)-1].stage
	b, _ := json.Marshal(map[string]any{"id": id, "company": company, "role": role, "stage": last,
		"open": last != "rejected" && last != "offer", "applied_at": tl[0].At, "timeline": tl, "accounts": []string{"me@yahoo.com"}})
	return b
}

func newServer(t *testing.T, core *fakeCore) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "orgs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return serve(t, st, core, "owner"), st
}

func serve(t *testing.T, st *store.Store, core *fakeCore, user string) *httptest.Server {
	s := &Server{Store: st, Core: core, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), InternalToken: token,
		UserID: user, Now: func() time.Time { return now }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func do(t *testing.T, method, url, auth string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
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

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

func TestOrganizationsGroupByCompanyAndPreferAuto(t *testing.T) {
	core := &fakeCore{apps: []json.RawMessage{
		app(1, "Amazon", "PM", step{"applied", 40}, step{"rejected", 35}),
		app(2, "AMAZON", "Principal PM", step{"applied", 20}), // same organization, different case
		app(3, "Amazon", "SDM", step{"applied", 10}),
		app(4, "Google", "PM", step{"applied", 30}), app(5, "Google", "Staff PM", step{"applied", 25}),
		app(6, "Meta", "PM", step{"applied", 12}, step{"shortlisted", 5}, step{"interview", 5}),
		app(7, "Okta", "PM", step{"applied", 3}),
	}}
	ts, _ := newServer(t, core)
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	if rep.PreferredMode != "auto" || len(rep.Preferred) != 3 || rep.Preferred[0].Name != "Amazon" || rep.Preferred[0].Applications != 3 {
		t.Fatalf("preferred: %+v", rep.Preferred)
	}
	by := map[string]store.OrgSummary{}
	for _, o := range rep.Organizations {
		by[o.Name] = o
	}
	if a := by["Amazon"]; a.Rejected != 1 || a.AvgResponseDays == nil || *a.AvgResponseDays != 5 || !a.FirstAppliedAt.Equal(now.AddDate(0, 0, -40)) {
		t.Errorf("amazon: %+v", a)
	}
	if g := by["Google"]; !g.DiagnosisSuggested || g.DiagnosisReason != "No response to 2 applications" {
		t.Errorf("google: %+v", g)
	}
	if o := by["Okta"]; o.DiagnosisSuggested {
		t.Error("a 3-day-old application should not be diagnosed yet")
	}
	if m := by["Meta"]; m.DiagnosisSuggested || m.Interviewed != 1 {
		t.Errorf("meta: %+v", m)
	}

	// The same organizations keep their ids on the next request.
	again := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	for _, o := range again.Organizations {
		if by[o.Name].ID != o.ID {
			t.Errorf("%s changed id", o.Name)
		}
	}
	// Lookup by company name, as the pipeline links.
	if got := decodeBody[map[string]int64](t, do(t, "GET", ts.URL+"/api/v1/organizations/lookup?company=amazon", "", nil)); got["id"] != by["Amazon"].ID {
		t.Errorf("lookup: %v", got)
	}
	if res := do(t, "GET", ts.URL+"/api/v1/organizations/lookup?company=Nobody", "", nil); res.StatusCode != 404 {
		t.Errorf("unknown company: %d", res.StatusCode)
	}

	// Detail: the organization's applications as core-api reported them, newest first.
	det := decodeBody[struct {
		store.OrgSummary
		History []struct {
			ID   int64  `json:"id"`
			Role string `json:"role"`
		} `json:"application_history"`
	}](t, do(t, "GET", ts.URL+"/api/v1/organizations/"+itoa(by["Amazon"].ID), "", nil))
	if len(det.History) != 3 || det.History[0].Role != "SDM" || det.Applications != 3 {
		t.Errorf("detail: %+v", det)
	}

	// core-api down: a clear 502, not an empty page.
	core.err = errors.New("connection refused")
	if res := do(t, "GET", ts.URL+"/api/v1/organizations", "", nil); res.StatusCode != 502 {
		t.Errorf("core-api down: %d", res.StatusCode)
	}
}

func TestPreferredAndSuggestions(t *testing.T) {
	core := &fakeCore{apps: []json.RawMessage{
		app(1, "Acme", "PM", step{"applied", 20}, step{"rejected", 10}),
		app(2, "Beta", "PM", step{"applied", 30}, step{"shortlisted", 27}, step{"rejected", 20}),
	}}
	ts, st := newServer(t, core)
	rep := decodeBody[store.OrgsReport](t, do(t, "PUT", ts.URL+"/api/v1/organizations/preferred", "", map[string]any{"names": []string{"Walmart", "acme", "Acme"}}))
	if rep.PreferredMode != "manual" || len(rep.Preferred) != 2 || rep.Preferred[0].Name != "Walmart" || rep.Preferred[0].Applications != 0 {
		t.Fatalf("manual preferred: %+v", rep.Preferred)
	}
	sug := decodeBody[struct {
		Suggestions []store.Suggestion `json:"suggestions"`
	}](t, do(t, "GET", ts.URL+"/api/v1/suggestions", "", nil)).Suggestions
	if len(sug) != 3 || sug[0].Name != "Walmart" || sug[2].Name != "Beta" {
		t.Errorf("suggestions: %+v", sug)
	}
	if res := do(t, "PUT", ts.URL+"/api/v1/organizations/preferred", "", map[string]any{"names": []string{"!!!"}}); res.StatusCode != 400 {
		t.Errorf("empty name accepted: %d", res.StatusCode)
	}

	walmart := itoa(rep.Preferred[0].ID)
	// Another user can't read this user's organizations.
	bob := serve(t, st, core, "bob")
	if res := do(t, "GET", bob.URL+"/api/v1/organizations/"+walmart, "", nil); res.StatusCode != 404 {
		t.Errorf("bob read alice's organization: %d", res.StatusCode)
	}
}
