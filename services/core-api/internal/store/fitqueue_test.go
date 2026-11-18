package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRefreshQueuesFitScores(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC()
	add := func(id, company, kind string) {
		if _, err := s.Ingest(ctx, "u", []IngestEvent{{MessageID: id, Account: "me@gmail.com", OccurredAt: at, Sender: "x@y.com",
			Kind: kind, Company: company, Role: "PM"}}); err != nil {
			t.Fatal(err)
		}
	}
	add("old", "Old", "applied")
	if _, err := s.RecordRefresh(ctx, "u", time.Now()); err != nil { // first refresh: no fit scores queued
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	add("n1", "New", "applied")
	add("r1", "Gone", "applied")
	add("r2", "Gone", "rejected") // already rejected: not worth scoring
	add("u1", "Lonely", "rejected")
	time.Sleep(5 * time.Millisecond)
	if _, err := s.RecordRefresh(ctx, "u", time.Now()); err != nil {
		t.Fatal(err)
	}
	list, _ := s.PendingResearch(ctx, "u")
	fits := []string{}
	for _, r := range list {
		if r.Kind == "fit_score" {
			fits = append(fits, r.Name)
		}
	}
	if len(fits) != 1 || fits[0] != "New — PM" {
		t.Fatalf("fit queue: %v (all %+v)", fits, list)
	}
	// Saving a fit score marks the request done.
	var appID int64
	s.db.QueryRow(`SELECT id FROM applications WHERE company = 'New'`).Scan(&appID)
	f := Fit{Total: 50, Model: "claude-opus-5-5", Factors: []FitFactor{{"experience", 10, ""}, {"location", 10, ""}, {"seniority", 10, ""},
		{"role_alignment", 10, ""}, {"interest_intent", 10, ""}}}
	if err := s.SaveFit(ctx, "u", appID, f, time.Now()); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Detail(ctx, "u", appID); d.FitRequestedAt != nil {
		t.Error("fit request still pending after saving")
	}
	if _, err := s.RequestResearch(ctx, "u", "fit_score", appID, time.Now()); err != nil {
		t.Errorf("manual fit request: %v", err)
	}
	if d, _ := s.Detail(ctx, "u", appID); d.FitRequestedAt == nil {
		t.Error("manual fit request not shown")
	}
}

func TestFitBand(t *testing.T) {
	for total, want := range map[int]string{100: "strong", 80: "strong", 79: "good", 75: "good", 74: "moderate", 60: "moderate", 59: "weak", 0: "weak"} {
		if got := FitBand(total); got != want {
			t.Errorf("FitBand(%d) = %q, want %q", total, got, want)
		}
	}
}
