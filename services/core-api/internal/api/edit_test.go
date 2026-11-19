package api

import (
	"testing"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func TestEditApplicationDetails(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("u1", "Unknown company", "", "applied", now.AddDate(0, 0, -10)))
	res := do(t, "PATCH", ts.URL+"/api/v1/applications/1", "", map[string]string{
		"company": "Accenture", "role": "AI Technology Architect", "job_url": "https://www.accenture.com/in-en/careers/jobdetails?id=R00123"})
	out := decodeBody[struct{ ID int64 }](t, res)
	if res.StatusCode != 200 || out.ID != 1 {
		t.Fatalf("edit: %d %+v", res.StatusCode, out)
	}
	app := decodeBody[struct{ Application store.Application }](t, do(t, "GET", ts.URL+"/api/v1/applications/1", "", nil)).Application
	if app.Company != "Accenture" || app.Role != "AI Technology Architect" || app.JobURL == "" {
		t.Errorf("app: %+v", app)
	}
	det := decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/1/detail", "", nil))
	if len(det.JobLinks) != 1 || det.JobLinks[0] != app.JobURL {
		t.Errorf("job links: %+v", det.JobLinks)
	}
	// A later email for the corrected company and role finds the edited application.
	ingest(t, ts, ev("u2", "Accenture", "AI Technology Architect", "rejected", now))
	app = decodeBody[struct{ Application store.Application }](t, do(t, "GET", ts.URL+"/api/v1/applications/1", "", nil)).Application
	if app.Stage != "rejected" || app.EventCount != 2 {
		t.Errorf("follow-up email did not attach: %+v", app)
	}
}

func TestEditMergesIntoExistingApplication(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts,
		ev("a1", "Acme", "PM", "applied", now.AddDate(0, 0, -20)),
		ev("u1", "Unknown company", "", "rejected", now.AddDate(0, 0, -5)))
	res := do(t, "PATCH", ts.URL+"/api/v1/applications/2", "", map[string]string{"company": "Acme", "role": "PM"})
	out := decodeBody[struct{ ID int64 }](t, res)
	if out.ID != 1 {
		t.Fatalf("want merge into application 1, got %+v", out)
	}
	apps := decodeBody[struct{ Applications []store.Application }](t, do(t, "GET", ts.URL+"/api/v1/applications", "", nil)).Applications
	if len(apps) != 1 || apps[0].Stage != "rejected" || apps[0].EventCount != 2 {
		t.Errorf("after merge: %+v", apps)
	}
}

func TestEditValidationAndScope(t *testing.T) {
	ts, st := newServer(t, "alice")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now))
	for _, body := range []map[string]string{
		{"job_url": "javascript:alert(1)"}, {"job_url": "http://jobs.example/1"}, {"job_url": "https://localhost/x"},
		{"job_url": "https://192.168.1.5/jd"}, {"job_url": "https://user:pw@jobs.example/1"}, {"company": "!!!"},
	} {
		if res := do(t, "PATCH", ts.URL+"/api/v1/applications/1", "", body); res.StatusCode != 400 {
			t.Errorf("%v accepted: %d", body, res.StatusCode)
		}
	}
	bob := newServerFor(t, st, "bob")
	if res := do(t, "PATCH", bob.URL+"/api/v1/applications/1", "", map[string]string{"company": "Evil"}); res.StatusCode != 404 {
		t.Errorf("bob edited alice's application: %d", res.StatusCode)
	}
	// Removing the link is allowed.
	if res := do(t, "PATCH", ts.URL+"/api/v1/applications/1", "", map[string]string{"job_url": ""}); res.StatusCode != 200 {
		t.Errorf("clear link: %d", res.StatusCode)
	}
}
