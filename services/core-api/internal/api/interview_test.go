package api

import (
	"strconv"
	"testing"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

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

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
