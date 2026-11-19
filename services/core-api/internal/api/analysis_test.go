package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func TestAnalysisReport(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	ingest(t, ts,
		ev("a1", "Acme", "PM", "applied", d(40)), ev("a2", "Acme", "PM", "interview", d(30)), ev("a3", "Acme", "PM", "rejected", d(20)),
		ev("b1", "Globex", "PM", "applied", d(16)),
		ev("c1", "Initech", "PM", "applied", d(10)), ev("c2", "Initech", "PM", "offer", d(2)))
	rep := decodeBody[store.AnalysisReport](t, do(t, "GET", ts.URL+"/api/v1/analysis", "", nil))
	if len(rep.Applications) != 3 {
		t.Fatalf("apps: %d", len(rep.Applications))
	}
	by := map[string]store.AppAnalysis{}
	for _, a := range rep.Applications {
		by[a.Company] = a
	}
	acme := by["Acme"]
	if acme.Open || acme.Outcome != "rejected" || acme.DaysToOutcome == nil || *acme.DaysToOutcome != 20 {
		t.Errorf("Acme: open=%v outcome=%q days=%v", acme.Open, acme.Outcome, acme.DaysToOutcome)
	}
	if len(acme.Timeline) != 4 || acme.Timeline[2].Label != "Interview 1" || *acme.Timeline[1].DaysFromPrev != 10 {
		t.Errorf("Acme timeline: %+v", acme.Timeline)
	}
	if g := by["Globex"]; !g.Open || g.DaysInStage != 16 || g.NextMove.Title != "Send a follow-up" {
		t.Errorf("Globex: %+v", g)
	}
	if rep.Funnel.Applied != 3 || rep.Funnel.Offers != 1 || rep.Funnel.Rejected != 1 || rep.Funnel.Responded != 2 {
		t.Errorf("funnel: %+v", rep.Funnel)
	}
	var applied *store.DurationStat
	for i := range rep.StageDwell {
		if rep.StageDwell[i].Key == "applied" {
			applied = &rep.StageDwell[i]
		}
	}
	// Acme waited 10 days before shortlisting; Initech 8 days before the offer.
	if applied == nil || applied.N != 2 || applied.AvgDays != 9 {
		t.Errorf("applied dwell: %+v", applied)
	}
}

func TestFitAndPostingRoundTrip(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now))
	min := 8
	posting := store.Posting{Status: "found", Source: "web_search", URL: "https://jobs.acme.example/pm", Title: "Principal PM",
		MinYears: &min, Requirements: []string{"8+ years PM"}, Responsibilities: []string{"Own the roadmap"}, Qualifications: []string{}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/applications/1/posting", "Bearer "+token, posting); res.StatusCode != 204 {
		t.Fatalf("posting: %d", res.StatusCode)
	}
	fit := store.Fit{Total: 70, Verdict: "Strong", Model: "claude-opus-5-5", Suggestions: []string{"Lead with growth metrics"},
		Pipeline:  "retrieval=claude-sonnet-4-6;summary=claude-sonnet-5;correlation=claude-opus-5-5;scoring=claude-opus-5-5",
		Strengths: []string{"Growth PM at scale"}, Gaps: []string{"No fintech"},
		Evidence: []store.Evidence{{Requirement: "8+ years PM", Evidence: "12 years", Strength: "strong"}, {Requirement: "Payments", Evidence: "", Strength: "missing"}},
		Factors:  []store.FitFactor{ff("experience", 16), ff("location", 18), ff("seniority", 14), ff("role_alignment", 12), ff("interest_intent", 10)}}
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/1/fit", "Bearer "+token, fit); res.StatusCode != 204 {
		t.Fatalf("fit: %d", res.StatusCode)
	}
	det := decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/1/detail", "", nil))
	if det.Posting == nil || det.Posting.Title != "Principal PM" || len(det.Posting.Requirements) != 1 || *det.Posting.MinYears != 8 {
		t.Errorf("posting: %+v", det.Posting)
	}
	if det.Fit == nil || det.Fit.Total != 70 || det.Fit.Band != "moderate" || len(det.Fit.Factors) != 5 || det.Fit.Factors[0].Factor != "experience" || len(det.Fit.Suggestions) != 1 {
		t.Errorf("fit: %+v", det.Fit)
	}
	if len(det.Fit.Evidence) != 2 || det.Fit.Evidence[1].Strength != "missing" || det.Fit.Gaps[0] != "No fintech" ||
		!strings.Contains(det.Fit.Pipeline, "summary=claude-sonnet-5") {
		t.Errorf("router fields: %+v", det.Fit)
	}
	fit.Evidence[0].Strength = "excellent"
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/1/fit", "Bearer "+token, fit); res.StatusCode != 400 {
		t.Errorf("invalid evidence strength accepted: %d", res.StatusCode)
	}
	rep := decodeBody[store.AnalysisReport](t, do(t, "GET", ts.URL+"/api/v1/analysis", "", nil))
	if rep.Applications[0].Fit == nil || rep.Applications[0].Fit.Total != 70 || rep.Applications[0].Fit.Band != "moderate" || rep.Applications[0].PostingURL != posting.URL {
		t.Errorf("analysis fit summary: %+v", rep.Applications[0])
	}
}

func TestFitValidation(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now))
	five := []store.FitFactor{ff("experience", 10), ff("location", 10), ff("seniority", 10), ff("role_alignment", 10), ff("interest_intent", 10)}
	bad := []store.Fit{
		{Total: 60, Model: "m", Factors: five},                                                     // total != sum
		{Total: 40, Model: "m", Factors: five[:4]},                                                 // missing factor
		{Total: 50, Model: "m", Factors: append([]store.FitFactor{ff("salary", 10)}, five[1:]...)}, // unknown factor
		{Total: 50, Model: "m", Factors: []store.FitFactor{ff("experience", 30), ff("location", 5), ff("seniority", 5), ff("role_alignment", 5), ff("interest_intent", 5)}},
	}
	for i, f := range bad {
		if res := do(t, "POST", ts.URL+"/internal/v1/applications/1/fit", "Bearer "+token, f); res.StatusCode != 400 {
			t.Errorf("case %d: want 400, got %d", i, res.StatusCode)
		}
	}
}

func TestOutreachAndContacts(t *testing.T) {
	ts, _ := newServer(t, "owner")
	e := ev("a1", "Acme", "PM", "interview", now)
	e.Sender, e.SenderName = "priya@acme.example", "Priya Rao"
	robot := ev("a2", "Acme", "PM", "applied", now.AddDate(0, 0, -5))
	robot.Sender = "no-reply@acme.example"
	ingest(t, ts, e, robot)
	det := decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/1/detail", "", nil))
	if len(det.Contacts) != 1 || det.Contacts[0].Name != "Priya Rao" {
		t.Fatalf("contacts: %+v", det.Contacts)
	}
	cid := det.Contacts[0].ID
	draft := store.OutreachDraft{ContactID: &cid, Subject: "Hello", Body: "Hi Priya", ProductAnalysis: "p", Model: "claude-opus-5-5",
		Experience: []string{"e1"}, ValueAdds: []string{"v1"}, Improvements: []string{"i1", "i2"}}
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/1/outreach", "Bearer "+token, draft); res.StatusCode != 201 {
		t.Fatalf("outreach: %d", res.StatusCode)
	}
	det = decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/1/detail", "", nil))
	if len(det.Drafts) != 1 || len(det.Drafts[0].Improvements) != 2 || *det.Drafts[0].ContactID != cid {
		t.Errorf("drafts: %+v", det.Drafts)
	}
	other := int64(999)
	draft.ContactID = &other
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/1/outreach", "Bearer "+token, draft); res.StatusCode != 400 {
		t.Errorf("foreign contact accepted: %d", res.StatusCode)
	}
}

func TestProfileRoundTripAndValidation(t *testing.T) {
	ts, _ := newServer(t, "owner")
	p := store.Profile{FullName: "Test User", TotalYears: 12, Skills: []string{"Growth", "AI"},
		Experiences: []store.Experience{{Title: "Principal PM", Company: "New Relic", StartMonth: "2024-09", Highlights: []string{"Grew X by 30%"}}}}
	if res := do(t, "PUT", ts.URL+"/api/v1/profile", "", p); res.StatusCode != 200 {
		t.Fatalf("put: %d", res.StatusCode)
	}
	got := decodeBody[store.Profile](t, do(t, "GET", ts.URL+"/api/v1/profile", "", nil))
	if got.TotalYears != 12 || len(got.Skills) != 2 || len(got.Experiences) != 1 || got.Experiences[0].Highlights[0] != "Grew X by 30%" {
		t.Errorf("got %+v", got)
	}
	p.Experiences[0].StartMonth = "Sep 2024"
	if res := do(t, "PUT", ts.URL+"/api/v1/profile", "", p); res.StatusCode != 400 {
		t.Errorf("bad month accepted: %d", res.StatusCode)
	}
}

// Security: agent write endpoints need the internal token; detail is scoped to the user.
func TestAnalysisSecurity(t *testing.T) {
	ts, st := newServer(t, "alice")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now))
	for _, path := range []string{"/internal/v1/applications/1/fit", "/internal/v1/applications/1/outreach", "/internal/v1/llm-usage"} {
		if res := do(t, "POST", ts.URL+path, "", `{}`); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without token: %d", path, res.StatusCode)
		}
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/applications/1/posting", "Bearer wrong", `{}`); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("posting with bad token: %d", res.StatusCode)
	}
	bob := newServerFor(t, st, "bob")
	if res := do(t, "GET", bob.URL+"/api/v1/applications/1/detail", "", nil); res.StatusCode != 404 {
		t.Errorf("bob read alice's detail: %d", res.StatusCode)
	}
	if res := do(t, "PUT", bob.URL+"/internal/v1/applications/1/posting", "Bearer "+token,
		store.Posting{Status: "found", Source: "none"}); res.StatusCode != 404 {
		t.Errorf("bob wrote alice's posting: %d", res.StatusCode)
	}
	rep := decodeBody[store.AnalysisReport](t, do(t, "GET", bob.URL+"/api/v1/analysis", "", nil))
	if len(rep.Applications) != 0 {
		t.Errorf("bob's analysis leaked: %+v", rep)
	}
}

// newServerFor serves another user on the same database.
func newServerFor(t *testing.T, st *store.Store, userID string) *httptest.Server {
	t.Helper()
	s := &Server{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		InternalToken: token, UserID: userID, Now: func() time.Time { return now }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func ff(name string, score int) store.FitFactor {
	return store.FitFactor{Factor: name, Score: score, Rationale: "because"}
}

func TestReimportBackfillsLinksAndNames(t *testing.T) {
	ts, _ := newServer(t, "owner")
	e := ev("a1", "Acme", "PM", "interview", now)
	e.Sender = "priya@acme.example"
	ingest(t, ts, e)
	e.SenderName, e.JobURL = "Priya Rao", "https://jobs.lever.co/acme/4f1c2d3e-aaaa-bbbb-cccc-1234567890ab"
	ingest(t, ts, e) // same message again, now with more detail
	det := decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/1/detail", "", nil))
	if len(det.JobLinks) != 1 || det.JobLinks[0] != e.JobURL {
		t.Errorf("links: %+v", det.JobLinks)
	}
	if len(det.Contacts) != 1 || det.Contacts[0].Name != "Priya Rao" {
		t.Errorf("contacts: %+v", det.Contacts)
	}
	bad := ev("a2", "Acme", "PM", "applied", now)
	bad.JobURL = "javascript:alert(1)"
	res := do(t, "POST", ts.URL+"/internal/v1/events", "Bearer "+token, map[string]any{"events": []store.IngestEvent{bad}})
	if res.StatusCode != 400 {
		t.Errorf("non-https job_url accepted: %d", res.StatusCode)
	}
}
