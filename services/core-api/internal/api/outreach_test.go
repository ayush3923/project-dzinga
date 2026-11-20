package api

import (
	"testing"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

// "Draft cold email" queues the email for Claude; the tile shows it queued, then
// ready once Claude saves the draft, until the user opens it.
func TestColdEmailIsQueuedThenFlaggedUntilOpened(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("p1", "People Inc.", "Senior Product Manager", "applied", now))
	id := apps(t, ts.URL)[0].ID
	path := ts.URL + "/api/v1/applications/" + itoa(id)
	state := func() string { return apps(t, ts.URL)[0].DraftState }

	if res := do(t, "POST", path+"/outreach-request", "", map[string]any{"recipient": "Priya Sharma, HR"}); res.StatusCode != 202 {
		t.Fatalf("queue: %d", res.StatusCode)
	}
	if res := do(t, "POST", path+"/outreach-request", "", map[string]any{"contact_id": 999}); res.StatusCode != 400 {
		t.Errorf("someone else's contact accepted: %d", res.StatusCode)
	}
	if state() != "queued" {
		t.Errorf("after queueing: %q", state())
	}
	q := decodeBody[struct{ Requests []store.ResearchRequest }](t, do(t, "GET", ts.URL+"/internal/v1/research-requests", "Bearer "+token, nil)).Requests
	if len(q) != 1 || q[0].Kind != "outreach" || q[0].Recipient != "Priya Sharma, HR" {
		t.Fatalf("queue: %+v", q)
	}
	det := decodeBody[store.AppDetail](t, do(t, "GET", path+"/detail", "", nil))
	if det.OutreachRequestedAt == nil || det.OutreachRecipient != "Priya Sharma, HR" {
		t.Errorf("detail: %+v %q", det.OutreachRequestedAt, det.OutreachRecipient)
	}

	draft := store.OutreachDraft{Recipient: "Priya Sharma, HR", Subject: "JR15744: a quick question", Body: "Hi Priya, …", Model: "claude-opus-5-5"}
	if res := do(t, "POST", ts.URL+"/internal/v1/applications/"+itoa(id)+"/outreach", "Bearer "+token, draft); res.StatusCode != 201 {
		t.Fatalf("save draft: %d", res.StatusCode)
	}
	if state() != "ready" {
		t.Errorf("after the draft: %q", state())
	}
	det = decodeBody[store.AppDetail](t, do(t, "GET", path+"/detail", "", nil))
	if det.OutreachRequestedAt != nil || len(det.Drafts) != 1 || !det.Drafts[0].New || det.Drafts[0].Recipient != "Priya Sharma, HR" {
		t.Errorf("detail after the draft: %+v", det)
	}
	if res := do(t, "POST", path+"/drafts/seen", "", nil); res.StatusCode != 204 {
		t.Fatalf("seen: %d", res.StatusCode)
	}
	if state() != "drafted" {
		t.Errorf("after opening: %q", state())
	}
}
