package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestResearchQueue(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	at0 := now.AddDate(0, 0, -3).Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at)
		VALUES ('owner', 'acme|senior pm', 'Acme', 'Senior PM', 'applied', 'me@gmail.com', ?, ?)`, at0, at0)
	if err != nil {
		t.Fatal(err)
	}
	appID, _ := res.LastInsertId()

	if _, err := s.RequestResearch(ctx, "owner", "job_posting", appID, now); err != nil {
		t.Fatal(err)
	}
	// Asking twice keeps one pending request, with the first time.
	at, err := s.RequestResearch(ctx, "owner", "job_posting", appID, now.Add(time.Hour))
	if err != nil || !at.Equal(now) {
		t.Fatalf("at=%v err=%v", at, err)
	}
	if _, err := s.RequestResearch(ctx, "owner", "fit_score", appID, now); err != nil {
		t.Fatal(err)
	}
	// Another user's targets and unknown kinds are refused.
	if _, err := s.RequestResearch(ctx, "intruder", "job_posting", appID, now); err != ErrNotFound {
		t.Errorf("other user: %v", err)
	}
	// Company profiles belong to the organizations service now (ADR 0010).
	for _, k := range []string{"delete_everything", "company_profile"} {
		if _, err := s.RequestResearch(ctx, "owner", k, appID, now); err != ErrBadKind {
			t.Errorf("kind %s: %v", k, err)
		}
	}
	list, err := s.PendingResearch(ctx, "owner")
	if err != nil || len(list) != 2 || list[0].Name == "" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	d, _ := s.Detail(ctx, "owner", appID)
	if d.ResearchRequestedAt == nil {
		t.Error("detail does not show the pending request")
	}

	// Saving the results completes the requests.
	if err := s.SavePosting(ctx, "owner", appID, Posting{Status: "found", Source: "web_search", URL: "https://jobs.example/1", Title: "Senior PM"}, now); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.PendingResearch(ctx, "owner"); len(list) != 1 || list[0].Kind != "fit_score" {
		t.Errorf("still pending: %+v", list)
	}
}
