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

// Correcting an application into an existing one carries its pending research across,
// so the queue never lists an application that no longer exists.
func TestMergeMovesPendingResearch(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	at := time.Now().UTC()
	for _, e := range []IngestEvent{
		{MessageID: "w1", Account: "me@gmail.com", OccurredAt: at.AddDate(0, 0, -30), Sender: "x@y.com", Kind: "applied", Company: "Walmart", Role: "Staff PM"},
		{MessageID: "w2", Account: "me@gmail.com", OccurredAt: at, Sender: "x@y.com", Kind: "applied", Company: "Unknown company"},
	} {
		if _, err := s.Ingest(ctx, "u", []IngestEvent{e}); err != nil {
			t.Fatal(err)
		}
	}
	var walmart, unknown int64
	s.db.QueryRow(`SELECT id FROM applications WHERE company = 'Walmart'`).Scan(&walmart)
	s.db.QueryRow(`SELECT id FROM applications WHERE company = 'Unknown company'`).Scan(&unknown)
	for _, id := range []int64{walmart, unknown} {
		if _, err := s.RequestResearch(ctx, "u", "fit_score", id, at); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RequestResearch(ctx, "u", "job_posting", unknown, at); err != nil {
		t.Fatal(err)
	}
	company, role := "Walmart", "Staff PM"
	if got, err := s.EditApplication(ctx, "u", unknown, ApplicationEdit{Company: &company, Role: &role}); err != nil || got != walmart {
		t.Fatalf("merge: id %d, err %v", got, err)
	}
	list, _ := s.PendingResearch(ctx, "u")
	kinds := map[string]int64{}
	for _, r := range list {
		kinds[r.Kind] = r.TargetID
	}
	if len(list) != 2 || kinds["fit_score"] != walmart || kinds["job_posting"] != walmart {
		t.Fatalf("queue after merge: %+v", list)
	}
}
