package api

import (
	"testing"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func TestFitRequest(t *testing.T) {
	ts, _ := newServer(t, "owner")
	ingest(t, ts, ev("a1", "Acme", "PM", "applied", now))
	if res := do(t, "POST", ts.URL+"/api/v1/applications/1/fit-request", "", nil); res.StatusCode != 202 {
		t.Fatalf("fit request: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/api/v1/applications/99/fit-request", "", nil); res.StatusCode != 404 {
		t.Errorf("unknown app: %d", res.StatusCode)
	}
	d := decodeBody[store.AppDetail](t, do(t, "GET", ts.URL+"/api/v1/applications/1/detail", "", nil))
	if d.FitRequestedAt == nil {
		t.Error("fit request not shown in detail")
	}
}
