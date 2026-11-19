package api

import (
	"testing"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func TestBriefingFlagsOrganizationUntilSeen(t *testing.T) {
	ts, st := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now.AddDate(0, 0, -5)))
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
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/briefing", "Bearer "+token, b); res.StatusCode != 204 {
		t.Fatalf("briefing: %d", res.StatusCode)
	}
	// The same link again is kept once.
	b.News = b.News[:1]
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/briefing", "Bearer "+token, b); res.StatusCode != 204 {
		t.Fatalf("briefing again: %d", res.StatusCode)
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
	rep = decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	if rep.Organizations[0].BriefingUnseen {
		t.Error("still flagged after opening")
	}

	// Validation and security.
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
	bob := newServerFor(t, st, "bob")
	if res := do(t, "PUT", bob.URL+"/internal/v1/organizations/"+id+"/briefing", "Bearer "+token, b); res.StatusCode != 404 {
		t.Errorf("bob wrote alice's briefing: %d", res.StatusCode)
	}
	if res := do(t, "POST", bob.URL+"/api/v1/organizations/"+id+"/briefing/seen", "", nil); res.StatusCode != 404 {
		t.Errorf("bob marked alice's briefing: %d", res.StatusCode)
	}
}

func TestInterviewNotesQueueAReview(t *testing.T) {
	ts, st := newServer(t, "owner")
	ingest(t, ts,
		ev("a1", "Acme", "PM", "applied", now.AddDate(0, 0, -10)), ev("a2", "Acme", "PM", "interview", now.AddDate(0, 0, -2)),
		ev("b1", "Beta", "PM", "applied", now.AddDate(0, 0, -3)))
	apps := decodeBody[struct {
		Applications []store.Application `json:"applications"`
	}](t, do(t, "GET", ts.URL+"/api/v1/applications", "", nil)).Applications
	ids := map[string]string{}
	for _, a := range apps {
		ids[a.Company] = itoa(a.ID)
	}

	note := store.InterviewNote{Round: 1, Body: "Case study on activation went well; they pushed on pricing."}
	res := do(t, "POST", ts.URL+"/api/v1/applications/"+ids["Acme"]+"/interview-notes", "", note)
	if res.StatusCode != 201 {
		t.Fatalf("note: %d", res.StatusCode)
	}
	if n := decodeBody[store.InterviewNote](t, res); n.ID == 0 || n.Round != 1 {
		t.Errorf("saved note: %+v", n)
	}
	if res := do(t, "POST", ts.URL+"/api/v1/applications/"+ids["Beta"]+"/interview-notes", "", note); res.StatusCode != 409 {
		t.Errorf("note on a non-interviewing application: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/api/v1/applications/"+ids["Acme"]+"/interview-notes", "", store.InterviewNote{Body: "ok"}); res.StatusCode != 400 {
		t.Errorf("too-short note: %d", res.StatusCode)
	}

	q := decodeBody[struct {
		Requests []store.ResearchRequest `json:"requests"`
	}](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil)).Requests
	if len(q) != 1 || q[0].Kind != "interview_review" || q[0].Name != "Acme — PM" {
		t.Fatalf("queue: %+v", q)
	}

	rv := store.InterviewReview{Chance: "medium", Summary: "Solid round one; pricing depth is the open question.", Model: "claude-opus-5-5",
		Signals: []string{"Case study landed"}, Concerns: []string{"Pricing follow-ups"}, NextSteps: []string{"Prepare a pricing teardown"}}
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/"+ids["Acme"]+"/interview-review", "Bearer "+token, rv); res.StatusCode != 204 {
		t.Fatalf("review: %d", res.StatusCode)
	}
	det := decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/"+ids["Acme"]+"/detail", "", nil))
	if len(det.InterviewNotes) != 1 || det.InterviewReview == nil || det.InterviewReview.Chance != "medium" ||
		det.InterviewReview.NextSteps[0] != "Prepare a pricing teardown" || det.ReviewRequestedAt != nil {
		t.Errorf("detail: notes=%+v review=%+v queued=%v", det.InterviewNotes, det.InterviewReview, det.ReviewRequestedAt)
	}

	bad := rv
	bad.Chance = "certain"
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/"+ids["Acme"]+"/interview-review", "Bearer "+token, bad); res.StatusCode != 400 {
		t.Errorf("bad chance accepted: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/"+ids["Acme"]+"/interview-review", "", rv); res.StatusCode != 401 {
		t.Errorf("review without token: %d", res.StatusCode)
	}
	bob := newServerFor(t, st, "bob")
	if res := do(t, "POST", bob.URL+"/api/v1/applications/"+ids["Acme"]+"/interview-notes", "", note); res.StatusCode != 404 {
		t.Errorf("bob wrote a note on alice's application: %d", res.StatusCode)
	}
}
