package peers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const tok = "test-token-0123456789abcdef0123456789"

func TestPeersSpeakEachServicesAPI(t *testing.T) {
	syncRunning := true
	mux := http.NewServeMux()
	auth := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+tok {
				w.WriteHeader(401)
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /api/v1/organizations/{id}", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "7" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`{"id":7,"name":"Acme","applications":2}`))
	}))
	mux.HandleFunc("GET /api/v1/organizations", auth(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"organizations":[{"id":7,"name":"Acme","applications":2,"last_activity_at":"2026-11-19T10:00:00Z"}]}`))
	}))
	mux.HandleFunc("POST /v1/sync", auth(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(409) // already running
		w.Write([]byte(`{"error":"a sync is already running"}`))
	}))
	mux.HandleFunc("GET /v1/sync", auth(func(w http.ResponseWriter, _ *http.Request) {
		if syncRunning {
			w.Write([]byte(`{"running":true,"mailboxes":[]}`))
			return
		}
		w.Write([]byte(`{"running":false,"mailboxes":[{"address":"me@yahoo.com","status":"done","added":3}]}`))
	}))
	mux.HandleFunc("POST /internal/v1/requests", auth(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(202) }))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()

	orgs := NewOrgs(srv.URL, tok)
	if o, err := orgs.Org(ctx, 7); err != nil || o.Name != "Acme" {
		t.Errorf("org: %+v %v", o, err)
	}
	if _, err := orgs.Org(ctx, 8); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing org: %v", err)
	}
	if c, err := orgs.Candidates(ctx); err != nil || len(c) != 1 || c[0].Applications != 2 || c[0].LastActivityAt == nil {
		t.Errorf("candidates: %+v %v", c, err)
	}

	ingest := NewIngest(srv.URL, tok)
	if err := ingest.Start(ctx); err != nil {
		t.Errorf("a refresh already running should be joined: %v", err)
	}
	syncRunning = false
	if err := ingest.Start(ctx); err == nil {
		t.Error("409 with nothing running should fail")
	}
	if st, err := ingest.State(ctx); err != nil || st.Running || st.Mailboxes[0].Added != 3 {
		t.Errorf("state: %+v %v", st, err)
	}
	if err := NewQuiz(srv.URL, tok).RequestToday(ctx); err != nil {
		t.Errorf("quiz: %v", err)
	}
	if err := NewQuiz(srv.URL, "wrong").RequestToday(ctx); err == nil {
		t.Error("quiz accepted a wrong token")
	}
}
