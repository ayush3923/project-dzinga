package api

import (
	"testing"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

type mailboxesResp struct {
	Mailboxes []store.Mailbox    `json:"mailboxes"`
	Settings  store.SyncSettings `json:"settings"`
}

func TestMailboxesAndSettings(t *testing.T) {
	ts, st := newServer(t, "owner")
	e := ev("a1", "Acme", "PM", "applied", now)
	e.Account = "Me@Yahoo.co.in"
	ingest(t, ts, e)
	if err := st.SeedMailboxes(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := decodeBody[mailboxesResp](t, do(t, "GET", ts.URL+"/api/v1/mailboxes", "", nil))
	if len(got.Mailboxes) != 1 || got.Mailboxes[0].Address != "me@yahoo.co.in" || got.Mailboxes[0].Provider != "yahoo" ||
		!got.Mailboxes[0].Enabled || got.Mailboxes[0].Emails != 1 || got.Settings.Lookback != "since_last" {
		t.Fatalf("seeded: %+v", got)
	}
	added := decodeBody[store.Mailbox](t, do(t, "POST", ts.URL+"/api/v1/mailboxes", "", map[string]string{"address": "someone@live.in"}))
	if added.Provider != "microsoft" || added.CredentialSaved {
		t.Errorf("added: %+v", added)
	}
	for _, bad := range []string{"not-an-address", "me@company.example", "Name <x@gmail.com>"} {
		if res := do(t, "POST", ts.URL+"/api/v1/mailboxes", "", map[string]string{"address": bad}); res.StatusCode != 400 {
			t.Errorf("%q accepted: %d", bad, res.StatusCode)
		}
	}
	if res := do(t, "PATCH", ts.URL+"/api/v1/mailboxes/"+itoa(added.ID), "", map[string]bool{"enabled": false}); res.StatusCode != 204 {
		t.Errorf("disable: %d", res.StatusCode)
	}
	yes, two := true, 2
	if res := do(t, "PUT", ts.URL+"/internal/v1/mailboxes/"+itoa(added.ID)+"/state", "Bearer "+token,
		store.MailboxState{CredentialSaved: &yes, Status: "ok", SyncedAt: &now, Added: &two}); res.StatusCode != 204 {
		t.Errorf("state: %d", res.StatusCode)
	}
	got = decodeBody[mailboxesResp](t, do(t, "GET", ts.URL+"/api/v1/mailboxes", "", nil))
	for _, m := range got.Mailboxes {
		if m.ID == added.ID && (m.Enabled || !m.CredentialSaved || m.LastStatus != "ok" || m.LastAdded != 2 || m.LastSyncedAt == nil) {
			t.Errorf("after state: %+v", m)
		}
	}

	s := decodeBody[store.SyncSettings](t, do(t, "PUT", ts.URL+"/api/v1/sync-settings", "", store.SyncSettings{Lookback: "custom", SinceDate: "2026-07-01"}))
	if s.SinceDate != "2026-07-01" {
		t.Errorf("settings: %+v", s)
	}
	for _, bad := range []store.SyncSettings{{Lookback: "forever"}, {Lookback: "custom", SinceDate: "July"}} {
		if res := do(t, "PUT", ts.URL+"/api/v1/sync-settings", "", bad); res.StatusCode != 400 {
			t.Errorf("%+v accepted: %d", bad, res.StatusCode)
		}
	}

	// Security: internal routes need the token; another user can't touch this mailbox.
	if res := do(t, "PUT", ts.URL+"/internal/v1/mailboxes/"+itoa(added.ID)+"/state", "", store.MailboxState{}); res.StatusCode != 401 {
		t.Errorf("state without token: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/maintenance", "", nil); res.StatusCode != 401 {
		t.Errorf("maintenance without token: %d", res.StatusCode)
	}
	bob := newServerFor(t, st, "bob")
	if res := do(t, "PATCH", bob.URL+"/api/v1/mailboxes/"+itoa(added.ID), "", map[string]bool{"enabled": true}); res.StatusCode != 404 {
		t.Errorf("bob changed alice's mailbox: %d", res.StatusCode)
	}
	if res := do(t, "DELETE", bob.URL+"/internal/v1/mailboxes/"+itoa(added.ID), "Bearer "+token, nil); res.StatusCode != 404 {
		t.Errorf("bob removed alice's mailbox: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/maintenance", "Bearer "+token, nil); res.StatusCode != 200 {
		t.Errorf("maintenance: %d", res.StatusCode)
	}
	// The digest is readable; only the agent (with the token) can write its text.
	if res := do(t, "GET", ts.URL+"/api/v1/digest", "", nil); res.StatusCode != 200 {
		t.Errorf("digest: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/digest", "", `{"refresh_id":1,"text":"x","model":"m"}`); res.StatusCode != 401 {
		t.Errorf("digest text without token: %d", res.StatusCode)
	}
}
