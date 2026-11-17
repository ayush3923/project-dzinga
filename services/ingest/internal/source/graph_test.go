package source

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeMicrosoft simulates the device-code sign-in and the Graph mail API.
func fakeMicrosoft(t *testing.T, signedInAs string) (*httptest.Server, *int32) {
	t.Helper()
	var polls, bodyFetches int32
	mux := http.NewServeMux()
	js := func(w http.ResponseWriter, v any) { json.NewEncoder(w).Encode(v) }
	auth := func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer tok-123" }

	mux.HandleFunc("POST /consumers/oauth2/v2.0/devicecode", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("client_id") != "client-abc" || !strings.Contains(r.Form.Get("scope"), "Mail.Read") {
			js(w, map[string]string{"error": "invalid_client"})
			return
		}
		js(w, map[string]any{"device_code": "dev-1", "user_code": "ABCD-1234",
			"verification_uri": "https://microsoft.com/devicelogin", "expires_in": 60, "interval": 1})
	})
	mux.HandleFunc("POST /consumers/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Form.Get("grant_type") == "refresh_token" {
			if r.Form.Get("refresh_token") != "rt-good" {
				js(w, map[string]string{"error": "invalid_grant"})
				return
			}
			js(w, map[string]string{"access_token": "tok-123", "refresh_token": "rt-rotated"})
			return
		}
		if atomic.AddInt32(&polls, 1) == 1 {
			js(w, map[string]string{"error": "authorization_pending"})
			return
		}
		js(w, map[string]string{"access_token": "tok-123", "refresh_token": "rt-first"})
	})
	mux.HandleFunc("GET /v1.0/me", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			w.WriteHeader(401)
			return
		}
		js(w, map[string]string{"userPrincipalName": signedInAs})
	})
	var srvURL string
	mux.HandleFunc("GET /v1.0/me/mailFolders/inbox/messages", func(w http.ResponseWriter, r *http.Request) {
		firstPage := r.URL.Query().Get("page") == ""
		if !auth(r) || (firstPage && !strings.HasPrefix(r.URL.Query().Get("$filter"), "receivedDateTime ge 2026-08-14")) {
			w.WriteHeader(400)
			return
		}
		msg := func(id, from, subj string) map[string]any {
			return map[string]any{"id": id, "internetMessageId": "<" + id + "@x>", "subject": subj,
				"receivedDateTime": "2026-09-01T10:00:00Z",
				"from":             map[string]any{"emailAddress": map[string]string{"name": "Acme Careers", "address": from}}}
		}
		if r.URL.Query().Get("page") == "2" {
			invite := msg("m4", "mom@example.org", "Ayush <> Acme") // a meeting request: read whatever the sender
			invite["@odata.type"] = "#microsoft.graph.eventMessageRequest"
			js(w, map[string]any{"value": []any{msg("m3", "jobs@acme.example", "Your application is received"), invite}})
			return
		}
		js(w, map[string]any{
			"value":           []any{msg("m1", "no-reply@greenhouse.io", "Thank you for applying"), msg("m2", "mom@example.org", "Dinner?")},
			"@odata.nextLink": srvURL + "/v1.0/me/mailFolders/inbox/messages?page=2",
		})
	})
	mux.HandleFunc("GET /v1.0/me/mailFolders/archive/messages", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404) // folder doesn't exist: skipped, not fatal
	})
	mux.HandleFunc("GET /v1.0/me/messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) || r.Header.Get("Prefer") != `outlook.body-content-type="text"` {
			w.WriteHeader(400)
			return
		}
		atomic.AddInt32(&bodyFetches, 1)
		js(w, map[string]any{"body": map[string]string{"contentType": "text", "content": "Body of " + r.PathValue("id")}})
	})
	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	t.Cleanup(srv.Close)
	return srv, &bodyFetches
}

func newGraph(srv *httptest.Server, account string) *Graph {
	return &Graph{
		ClientID: "client-abc", Account: account, Folders: []string{"inbox", "archive"},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Prompt: func(string, string) {},
		HTTP: srv.Client(), AuthBase: srv.URL + "/consumers", GraphBase: srv.URL + "/v1.0",
	}
}

func TestGraphFetch(t *testing.T) {
	srv, bodies := fakeMicrosoft(t, "Ayush@Live.in")
	g := newGraph(srv, "ayush@live.in")
	var prompted string
	g.Prompt = func(uri, code string) { prompted = uri + " " + code }

	var got []Message
	since := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	prefilter := func(from, subj string) bool { return from != "mom@example.org" }
	err := g.Fetch(context.Background(), since, prefilter, func(m Message) error {
		got = append(got, m)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompted != "https://microsoft.com/devicelogin ABCD-1234" {
		t.Errorf("prompt = %q", prompted)
	}
	if len(got) != 3 || *bodies != 3 {
		t.Fatalf("want 3 messages (personal mail filtered before download, invites kept), got %d, bodies fetched %d", len(got), *bodies)
	}
	if got[1].Invite || !got[2].Invite || got[2].ID != "m4@x" {
		t.Errorf("meeting request not marked: %+v", got[2])
	}
	if got[0].ID != "m1@x" || got[0].Text != "Body of m1" || got[0].FromName != "Acme Careers" || got[1].Subject != "Your application is received" {
		t.Errorf("unexpected messages: %+v", got)
	}
}

func TestGraphRejectsWrongAccount(t *testing.T) {
	srv, _ := fakeMicrosoft(t, "someone.else@outlook.com")
	err := newGraph(srv, "ayush@live.in").Fetch(context.Background(), time.Now(), nil, func(Message) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "signed in as") {
		t.Fatalf("want wrong-account error, got %v", err)
	}
}

func TestGraphBadClientID(t *testing.T) {
	srv, _ := fakeMicrosoft(t, "ayush@live.in")
	g := newGraph(srv, "ayush@live.in")
	g.ClientID = "wrong"
	if err := g.Fetch(context.Background(), time.Now(), nil, func(Message) error { return nil }); err == nil {
		t.Fatal("want error for bad client id")
	}
}

func TestGraphDeviceSignInReturnsRefreshToken(t *testing.T) {
	srv, _ := fakeMicrosoft(t, "ayush@live.in")
	g := newGraph(srv, "ayush@live.in")
	var saved string
	g.OnRefresh = func(rt string) { saved = rt }
	if err := g.Fetch(context.Background(), time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC), nil, func(Message) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if saved != "rt-first" {
		t.Errorf("refresh token not handed over: %q", saved)
	}
}

func TestGraphRefreshTokenSignsInSilently(t *testing.T) {
	srv, _ := fakeMicrosoft(t, "ayush@live.in")
	g := newGraph(srv, "ayush@live.in")
	g.RefreshToken = "rt-good"
	prompted := false
	g.Prompt = func(string, string) { prompted = true }
	var saved string
	g.OnRefresh = func(rt string) { saved = rt }
	n := 0
	if err := g.Fetch(context.Background(), time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC), nil, func(Message) error { n++; return nil }); err != nil {
		t.Fatal(err)
	}
	if prompted || saved != "rt-rotated" || n != 4 {
		t.Errorf("prompted=%v saved=%q messages=%d", prompted, saved, n)
	}
	g.RefreshToken = "rt-revoked"
	if err := g.Fetch(context.Background(), time.Now(), nil, func(Message) error { return nil }); err != ErrReconnect {
		t.Errorf("revoked token: want ErrReconnect, got %v", err)
	}
}
