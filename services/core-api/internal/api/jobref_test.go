package api

import (
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func withRef(e store.IngestEvent, ref string) store.IngestEvent { e.JobRef = ref; return e }

func apps(t *testing.T, url string) []store.AppAnalysis {
	t.Helper()
	return decodeBody[store.AnalysisReport](t, do(t, "GET", url+"/api/v1/analysis", "", nil)).Applications
}

// Two applications to one title at one company stay apart by requisition id
// (2026-11-19: Walmart's two Staff PM applications showed as one).
func TestRequisitionIdsSeparateApplications(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	ingest(t, ts,
		withRef(ev("w1", "Walmart", "Staff, Product Manager", "applied", d(36)), "R-2312345"),
		withRef(ev("w2", "Walmart", "Staff, Product Manager", "applied", d(2)), "r-2398765"), // ids are compared upper-case
		ev("w3", "Walmart", "Staff, Product Manager", "rejected", d(1)))                      // no id: the latest by then
	list := apps(t, ts.URL)
	if len(list) != 2 {
		t.Fatalf("want 2 applications, got %d: %+v", len(list), list)
	}
	by := map[int]store.AppAnalysis{}
	for _, a := range list {
		by[a.EventCount] = a
	}
	if by[1].Stage != "applied" || by[2].Stage != "rejected" {
		t.Errorf("the rejection should join the later application: %+v", list)
	}
	// The same id again joins its application.
	ingest(t, ts, withRef(ev("w4", "Walmart", "Staff, Product Manager", "interview", d(0)), "R-2312345"))
	if l := apps(t, ts.URL); len(l) != 2 {
		t.Errorf("same id made a new application: %d", len(l))
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/events", "Bearer "+token,
		map[string]any{"events": []store.IngestEvent{withRef(ev("w5", "Walmart", "PM", "applied", d(0)), "R 1; DROP")}}); res.StatusCode != 400 {
		t.Errorf("bad job_ref accepted: %d", res.StatusCode)
	}
}

// Mail read before ids were extracted merged both; a later sync fills the ids in and
// maintenance splits them, the first id keeping the original application.
func TestMaintenanceSplitsByRequisitionId(t *testing.T) {
	ts, _ := newServer(t, "owner")
	d := func(daysAgo int) time.Time { return now.AddDate(0, 0, -daysAgo) }
	a, b := ev("w1", "Walmart", "Staff, Product Manager", "applied", d(36)), ev("w2", "Walmart", "Staff, Product Manager", "applied", d(2))
	ingest(t, ts, a, b)
	list := apps(t, ts.URL)
	if len(list) != 1 {
		t.Fatalf("setup: %d applications", len(list))
	}
	first := list[0].ID
	ingest(t, ts, withRef(a, "R-2312345"), withRef(b, "R-2398765")) // re-read: fills the ids
	if res := do(t, "POST", ts.URL+"/internal/v1/maintenance", "Bearer "+token, nil); res.StatusCode != 200 {
		t.Fatalf("maintenance: %d", res.StatusCode)
	}
	list = apps(t, ts.URL)
	if len(list) != 2 {
		t.Fatalf("want 2 after the split, got %d", len(list))
	}
	for _, x := range list {
		if x.EventCount != 1 {
			t.Errorf("each should hold one email: %+v", x)
		}
		if x.ID == first && !x.AppliedAt.Equal(d(36)) {
			t.Errorf("the original application should keep the first id: %+v", x)
		}
	}
	// A third email for the second id goes straight to it.
	ingest(t, ts, withRef(ev("w3", "Walmart", "Staff, Product Manager", "interview", d(1)), "R-2398765"))
	for _, x := range apps(t, ts.URL) {
		if x.ID != first && (x.EventCount != 2 || x.Stage != "interview") {
			t.Errorf("follow-up went astray: %+v", x)
		}
	}
}

// A manual stage earlier than the emails show replaces the steps after it, so the
// timeline (and the Organizations counts built on it) follow the correction.
func TestManualStageCorrectsTimeline(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Atlassian", "Principal TPM", "interview", now.AddDate(0, 0, -20)))
	if res := do(t, "PATCH", ts.URL+"/api/v1/applications/1", "", map[string]string{"stage": "applied"}); res.StatusCode != 204 {
		t.Fatalf("patch: %d", res.StatusCode)
	}
	a := apps(t, ts.URL)[0]
	if a.Stage != "applied" || len(a.Timeline) != 1 || a.Timeline[0].Stage != "applied" {
		t.Errorf("timeline: %+v", a.Timeline)
	}
}
