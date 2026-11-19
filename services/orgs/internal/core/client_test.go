package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplications(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/analysis" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"applications":[{"id":1,"company":"Acme"},{"id":2,"company":"Beta"}],"funnel":{}}`))
	}))
	defer ts.Close()
	apps, err := New(ts.URL, "tok").Applications(context.Background())
	if err != nil || len(apps) != 2 {
		t.Fatalf("apps=%s err=%v", apps, err)
	}
	if _, err := New(ts.URL, "wrong").Applications(context.Background()); err == nil {
		t.Error("an error status was accepted")
	}
}
