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

func TestPreferredSuggestionsAndResearch(t *testing.T) {
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
	if res := do(t, "POST", ts.URL+"/api/v1/organizations/"+walmart+"/research", "", nil); res.StatusCode != 202 {
		t.Fatalf("research: %d", res.StatusCode)
	}
	if res := do(t, "GET", ts.URL+"/internal/v1/research-requests", "", nil); res.StatusCode != 401 {
		t.Errorf("queue without token: %d", res.StatusCode)
	}
	q := decodeBody[struct {
		Requests []store.ResearchRequest `json:"requests"`
	}](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil)).Requests
	if len(q) != 1 || q[0].Kind != "company_profile" || q[0].Name != "Walmart" {
		t.Fatalf("queue: %+v", q)
	}
	p := store.CompanyProfile{Overview: "Walmart sells everything.", InterviewAngle: "Scale.", Model: "claude-opus-5-5"}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+walmart+"/profile", "Bearer "+token, p); res.StatusCode != 204 {
		t.Fatalf("profile: %d", res.StatusCode)
	}
	if q := decodeBody[struct {
		Requests []store.ResearchRequest `json:"requests"`
	}](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil)).Requests; len(q) != 0 {
		t.Errorf("saving the profile left the request: %+v", q)
	}

	// Another user can't read or write this user's organizations.
	bob := serve(t, st, core, "bob")
	if res := do(t, "GET", bob.URL+"/api/v1/organizations/"+walmart, "", nil); res.StatusCode != 404 {
		t.Errorf("bob read alice's organization: %d", res.StatusCode)
	}
	if res := do(t, "PUT", bob.URL+"/internal/v1/organizations/"+walmart+"/profile", "Bearer "+token, p); res.StatusCode != 404 {
		t.Errorf("bob wrote alice's profile: %d", res.StatusCode)
	}
}

func TestCompanyProfileAndDiagnosisRoundTrip(t *testing.T) {
	ts, _ := newServer(t, &fakeCore{apps: []json.RawMessage{app(1, "Acme", "PM", step{"applied", 20})}})
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	id := itoa(rep.Organizations[0].ID)
	score, count := 3.9, 1200
	p := store.CompanyProfile{Overview: "Acme makes rockets.", InterviewAngle: "Lead with growth.", Model: "claude-sonnet-5",
		News:     []store.NewsItem{{Title: "Acme raises", PublishedOn: "2026-09-01", URL: "https://news.example/acme"}},
		Products: []store.Product{{Name: "Rocket", Description: "Goes up"}}, Leaders: []store.Leader{{Name: "Wile E.", Title: "CEO"}},
		Ratings: []store.Rating{{Source: "glassdoor", Score: &score, Scale: 5, ReviewCount: &count, URL: "https://www.glassdoor.com/acme"},
			{Source: "gartner_peer_insights", Scale: 5}}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/profile", "Bearer "+token, p); res.StatusCode != 204 {
		t.Fatalf("profile: %d", res.StatusCode)
	}
	dg := store.Diagnosis{Summary: "Silent so far.", Model: "claude-sonnet-5",
		Causes: []store.DiagnosisItem{{Text: "Seniority mismatch", Confidence: "medium"}}, Actions: []string{"Find a referral"}}
	if res := do(t, "POST", ts.URL+"/internal/v1/organizations/"+id+"/diagnosis", "Bearer "+token, dg); res.StatusCode != 204 {
		t.Fatalf("diagnosis: %d", res.StatusCode)
	}
	det := decodeBody[store.OrgDetail](t, do(t, "GET", ts.URL+"/api/v1/organizations/"+id, "", nil))
	if det.Profile == nil || len(det.Profile.Ratings) != 2 || *det.Profile.Ratings[0].Score != 3.9 || det.Profile.Leaders[0].Name != "Wile E." {
		t.Errorf("profile: %+v", det.Profile)
	}
	if det.Diagnosis == nil || det.Diagnosis.Causes[0].Confidence != "medium" || det.Diagnosis.Actions[0] != "Find a referral" {
		t.Errorf("diagnosis: %+v", det.Diagnosis)
	}
	bad := p
	bad.News = []store.NewsItem{{Title: "x", URL: "javascript:alert(1)"}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/profile", "Bearer "+token, bad); res.StatusCode != 400 {
		t.Errorf("javascript link accepted: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/profile", "", p); res.StatusCode != 401 {
		t.Errorf("profile without token: %d", res.StatusCode)
	}
}

func TestBriefingFlagsOrganizationUntilSeen(t *testing.T) {
	core := &fakeCore{apps: []json.RawMessage{app(1, "Acme", "PM", step{"applied", 5})}}
	ts, st := newServer(t, core)
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	id := itoa(rep.Organizations[0].ID)
	if rep.Organizations[0].BriefingUnseen {
		t.Fatal("flagged before any briefing")
	}
	b := store.Briefing{CEO: "Wile E. Coyote", CPO: "Road Runner (Chief Product Officer)", Model: "claude-opus-5-5",
		RevenueYear:    store.Figure{Value: "$1.2B", Period: "FY2025 (Jan–Dec 2025)", URL: "https://ir.acme.example/fy25"},
		RevenueQuarter: store.Figure{Value: "$310M", Period: "Q3 FY2026 (Jul–Sep 2026)", URL: "https://ir.acme.example/q3"},
		News: []store.BriefingNews{
			{Day: "2026-10-14", Title: "Acme buys back stock", Summary: "A $1B buyback.", URL: "https://news.example/a", PublishedOn: "2026-10-10"},
			{Day: "2026-10-14", Title: "Acme names a COO", URL: "https://news.example/b"},
		}}
	for range 2 { // the same links twice are kept once
		if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/briefing", "Bearer "+token, b); res.StatusCode != 204 {
			t.Fatalf("briefing: %d", res.StatusCode)
		}
	}
	rep = decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	if !rep.Organizations[0].BriefingUnseen {
		t.Error("not flagged after a briefing")
	}
	det := decodeBody[store.OrgDetail](t, do(t, "GET", ts.URL+"/api/v1/organizations/"+id, "", nil))
	if det.Briefing == nil || det.Briefing.CEO != "Wile E. Coyote" || det.Briefing.RevenueQuarter.Value != "$310M" ||
		len(det.Briefing.News) != 2 || det.Briefing.News[0].Title != "Acme names a COO" { // undated: sorted by its quiz day
		t.Fatalf("detail briefing: %+v", det.Briefing)
	}
	if res := do(t, "POST", ts.URL+"/api/v1/organizations/"+id+"/briefing/seen", "", nil); res.StatusCode != 204 {
		t.Fatalf("seen: %d", res.StatusCode)
	}
	if rep = decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil)); rep.Organizations[0].BriefingUnseen {
		t.Error("still flagged after opening")
	}

	bad := b
	bad.News = []store.BriefingNews{{Day: "2026-10-14", Title: "Bad link here", URL: "javascript:alert(1)"}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/briefing", "Bearer "+token, bad); res.StatusCode != 400 {
		t.Errorf("javascript link accepted: %d", res.StatusCode)
	}
	bad = b
	bad.RevenueYear = store.Figure{Value: "$1B"}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/briefing", "Bearer "+token, bad); res.StatusCode != 400 {
		t.Errorf("revenue without period and source accepted: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/briefing", "", b); res.StatusCode != 401 {
		t.Errorf("briefing without token: %d", res.StatusCode)
	}
	bob := serve(t, st, core, "bob")
	if res := do(t, "POST", bob.URL+"/api/v1/organizations/"+id+"/briefing/seen", "", nil); res.StatusCode != 404 {
		t.Errorf("bob marked alice's briefing: %d", res.StatusCode)
	}
}

func TestRefreshQueuesFiveRandomDiagnoses(t *testing.T) {
	var apps []json.RawMessage
	for i := int64(1); i <= 8; i++ {
		apps = append(apps, app(i, "Company "+itoa(i), "PM", step{"applied", 20}))
	}
	ts, _ := newServer(t, &fakeCore{apps: apps})
	queue := func() []store.ResearchRequest {
		res := do(t, "POST", ts.URL+"/internal/v1/diagnosis-requests", "Bearer "+token, nil)
		if res.StatusCode != 200 {
			t.Fatalf("queue: %d", res.StatusCode)
		}
		return decodeBody[struct{ Queued []store.ResearchRequest }](t, res).Queued
	}
	first := queue()
	if len(first) != store.DiagnosesPerRefresh {
		t.Fatalf("queued %d, want %d", len(first), store.DiagnosesPerRefresh)
	}
	// Another refresh before Claude has worked the queue adds nothing.
	if again := queue(); len(again) != 0 {
		t.Fatalf("queue already full, but queued %+v", again)
	}
	pending := decodeBody[struct{ Requests []store.ResearchRequest }](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil))
	if len(pending.Requests) != 5 {
		t.Fatalf("pending: %d", len(pending.Requests))
	}

	// Saving a diagnosis completes its request and flags the organization until it is opened.
	id := itoa(first[0].TargetID)
	det := decodeBody[store.OrgDetail](t, do(t, "GET", ts.URL+"/api/v1/organizations/"+id, "", nil))
	if det.DiagnosisRequestedAt == nil {
		t.Error("detail doesn't show the queued diagnosis")
	}
	dg := store.Diagnosis{Summary: "Too early to tell.", Model: "claude-opus-5-5", Causes: []store.DiagnosisItem{}, Actions: []string{}}
	if res := do(t, "POST", ts.URL+"/internal/v1/organizations/"+id+"/diagnosis", "Bearer "+token, dg); res.StatusCode != 204 {
		t.Fatalf("diagnosis: %d", res.StatusCode)
	}
	flag := func() bool {
		rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
		for _, o := range rep.Organizations {
			if itoa(o.ID) == id {
				return o.DiagnosisUnseen && o.HasDiagnosis
			}
		}
		return false
	}
	if !flag() {
		t.Error("new diagnosis not flagged")
	}
	if res := do(t, "POST", ts.URL+"/api/v1/organizations/"+id+"/seen", "", nil); res.StatusCode != 204 {
		t.Fatalf("seen: %d", res.StatusCode)
	}
	if flag() {
		t.Error("still flagged after opening")
	}
	pending = decodeBody[struct{ Requests []store.ResearchRequest }](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil))
	if len(pending.Requests) != 4 {
		t.Errorf("diagnosis request not completed: %d pending", len(pending.Requests))
	}
	// The next refresh tops up by one, from organizations not queued or freshly diagnosed.
	next := queue()
	if len(next) != 1 {
		t.Fatalf("top-up queued %d, want 1", len(next))
	}
	for _, r := range first {
		if r.TargetID == next[0].TargetID {
			t.Errorf("picked an organization already queued or just diagnosed: %+v", next[0])
		}
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/diagnosis-requests", "", nil); res.StatusCode != 401 {
		t.Errorf("queue without token: %d", res.StatusCode)
	}
}

func TestUserCanAskForADiagnosis(t *testing.T) {
	ts, _ := newServer(t, &fakeCore{apps: []json.RawMessage{app(1, "Acme", "PM", step{"applied", 3})}})
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	id := itoa(rep.Organizations[0].ID)
	if res := do(t, "POST", ts.URL+"/api/v1/organizations/"+id+"/diagnosis-request", "", nil); res.StatusCode != 202 {
		t.Fatalf("request: %d", res.StatusCode)
	}
	// Alongside a profile request for the same organization.
	if res := do(t, "POST", ts.URL+"/api/v1/organizations/"+id+"/research", "", nil); res.StatusCode != 202 {
		t.Fatalf("research: %d", res.StatusCode)
	}
	pending := decodeBody[struct{ Requests []store.ResearchRequest }](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil))
	if len(pending.Requests) != 2 || pending.Requests[0].Kind != store.KindDiagnosis || pending.Requests[1].Kind != store.KindProfile {
		t.Errorf("pending: %+v", pending.Requests)
	}
}
