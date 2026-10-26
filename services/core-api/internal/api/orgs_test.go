package api

import (
	"strconv"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func TestOrganizationsGroupAndPreferAuto(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	ingest(t, ts,
		ev("a1", "Amazon", "PM", "applied", d(40)), ev("a2", "Amazon", "PM", "rejected", d(35)),
		ev("a3", "AMAZON", "Principal PM", "applied", d(20)), // same organization, different case
		ev("a4", "Amazon", "SDM", "applied", d(10)),
		ev("g1", "Google", "PM", "applied", d(30)), ev("g2", "Google", "Staff PM", "applied", d(25)),
		ev("m1", "Meta", "PM", "applied", d(12)), ev("m2", "Meta", "PM", "interview", d(5)),
		ev("o1", "Okta", "PM", "applied", d(3)))
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	if rep.PreferredMode != "auto" || len(rep.Preferred) != 3 || rep.Preferred[0].Name != "Amazon" || rep.Preferred[0].Applications != 3 {
		t.Fatalf("preferred: %+v", rep.Preferred)
	}
	by := map[string]store.OrgSummary{}
	for _, o := range rep.Organizations {
		by[o.Name] = o
	}
	amazon := by["Amazon"]
	if amazon.Rejected != 1 || amazon.Responded != 1 || amazon.AvgResponseDays == nil || *amazon.AvgResponseDays != 5 ||
		!amazon.FirstAppliedAt.Equal(d(40)) {
		t.Errorf("amazon: %+v", amazon)
	}
	if g := by["Google"]; !g.DiagnosisSuggested || g.DiagnosisReason != "No response to 2 applications" {
		t.Errorf("google diagnosis: %+v", g)
	}
	if o := by["Okta"]; o.DiagnosisSuggested {
		t.Error("a 3-day-old application should not be diagnosed yet")
	}
	if m := by["Meta"]; m.DiagnosisSuggested || m.Interviewed != 1 {
		t.Errorf("meta: %+v", m)
	}
}

func TestPreferredManualListAndDetail(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now.AddDate(0, 0, -20)), ev("a2", "Acme", "PM", "rejected", now.AddDate(0, 0, -10)))
	res := do(t, "PUT", ts.URL+"/api/v1/organizations/preferred", "", map[string]any{"names": []string{"Walmart", "acme", "Acme"}})
	rep := decodeBody[store.OrgsReport](t, res)
	if rep.PreferredMode != "manual" || len(rep.Preferred) != 2 || rep.Preferred[0].Name != "Walmart" || rep.Preferred[0].Applications != 0 {
		t.Fatalf("manual preferred: %+v", rep.Preferred)
	}
	acme := rep.Preferred[1]
	if !acme.DiagnosisSuggested || acme.DiagnosisReason != "Every reply so far was a rejection (1 rejection, 1 application)" {
		t.Errorf("acme: %+v", acme)
	}
	det := decodeBody[store.OrgDetail](t, do(t, "GET", ts.URL+"/api/v1/organizations/"+itoa(acme.ID), "", nil))
	if len(det.History) != 1 || det.History[0].Company != "Acme" || det.Applications != 1 || det.Profile != nil {
		t.Errorf("detail: %+v", det)
	}
	rep = decodeBody[store.OrgsReport](t, do(t, "PUT", ts.URL+"/api/v1/organizations/preferred", "", map[string]any{"auto": true}))
	if rep.PreferredMode != "auto" || len(rep.Preferred) != 1 || rep.Preferred[0].Name != "Acme" {
		t.Errorf("reset to auto: %+v", rep.Preferred)
	}
	if res := do(t, "PUT", ts.URL+"/api/v1/organizations/preferred", "", map[string]any{"names": []string{"!!!"}}); res.StatusCode != 400 {
		t.Errorf("empty name accepted: %d", res.StatusCode)
	}
}

func TestCompanyProfileAndDiagnosisRoundTrip(t *testing.T) {
	ts, st := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now.AddDate(0, 0, -20)))
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
	if det.Profile == nil || len(det.Profile.Ratings) != 2 || det.Profile.Ratings[0].Score == nil || *det.Profile.Ratings[0].Score != 3.9 ||
		det.Profile.Ratings[1].Score != nil || det.Profile.Leaders[0].Name != "Wile E." {
		t.Errorf("profile: %+v", det.Profile)
	}
	if det.Diagnosis == nil || det.Diagnosis.Causes[0].Confidence != "medium" || det.Diagnosis.Actions[0] != "Find a referral" {
		t.Errorf("diagnosis: %+v", det.Diagnosis)
	}

	// Validation and security.
	bad := p
	bad.News = []store.NewsItem{{Title: "x", URL: "javascript:alert(1)"}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/profile", "Bearer "+token, bad); res.StatusCode != 400 {
		t.Errorf("javascript link accepted: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/organizations/"+id+"/profile", "", p); res.StatusCode != 401 {
		t.Errorf("profile without token: %d", res.StatusCode)
	}
	bob := newServerFor(t, st, "bob")
	if res := do(t, "GET", bob.URL+"/api/v1/organizations/"+id, "", nil); res.StatusCode != 404 {
		t.Errorf("bob read alice's organization: %d", res.StatusCode)
	}
	if res := do(t, "POST", bob.URL+"/internal/v1/organizations/"+id+"/diagnosis", "Bearer "+token, dg); res.StatusCode != 404 {
		t.Errorf("bob wrote alice's diagnosis: %d", res.StatusCode)
	}
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }

// Each "thank you for applying" is its own application, unless two arrive within 2 days.
func TestRepeatApplicationsToOneCompany(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	linkedIn := ev("g5", "Google", "", "applied", d(10).Add(3*time.Hour))
	ingest(t, ts,
		ev("g1", "Google", "", "applied", d(60)),
		ev("g2", "Google", "", "applied", d(40)),
		ev("g3", "Google", "", "rejected", d(30)), // joins the latest earlier application (d40)
		ev("g4", "Google", "", "applied", d(10)),
		linkedIn, // same application as g4
		ev("g6", "Google", "Staff PM", "applied", d(5)))
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	if len(rep.Organizations) != 1 {
		t.Fatalf("orgs: %+v", rep.Organizations)
	}
	g := rep.Organizations[0]
	if g.Applications != 4 || g.Rejected != 1 || !g.FirstAppliedAt.Equal(d(60)) {
		t.Errorf("google: apps=%d rejected=%d first=%v", g.Applications, g.Rejected, g.FirstAppliedAt)
	}
	det := decodeBody[store.OrgDetail](t, do(t, "GET", ts.URL+"/api/v1/organizations/"+itoa(g.ID), "", nil))
	for _, a := range det.History {
		if a.AppliedAt.Equal(d(40)) && (a.Stage != "rejected" || a.EventCount != 2) {
			t.Errorf("d40 application: %+v", a)
		}
		if a.AppliedAt.Equal(d(10)) && a.EventCount != 2 {
			t.Errorf("d10 application should hold both confirmations: %+v", a)
		}
	}
}

// Microsoft returns newest mail first; a reply must still find its application.
func TestNewestFirstBatchGroupsCorrectly(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	ingest(t, ts,
		ev("p2", "Pearson", "", "rejected", d(9)), // arrives first
		ev("p1", "Pearson", "Advanced Specialist, Product Management", "applied", d(19)),
		ev("u2", "UiPath", "Principal Product Manager", "rejected", d(4)),
		ev("u1", "UiPath", "Principal Product Manager - Agentic", "applied", d(18)))
	rep := decodeBody[store.OrgsReport](t, do(t, "GET", ts.URL+"/api/v1/organizations", "", nil))
	for _, o := range rep.Organizations {
		if o.Applications != 1 || o.Rejected != 1 {
			t.Errorf("%s: applications=%d rejected=%d", o.Name, o.Applications, o.Rejected)
		}
	}
}
