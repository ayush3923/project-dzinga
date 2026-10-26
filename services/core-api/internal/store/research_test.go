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
	res, _ := s.db.Exec(`INSERT INTO organizations (user_id, key, name, created_at) VALUES ('owner', 'acme', 'Acme', ?)`, at0)
	orgID, _ := res.LastInsertId()
	res, err = s.db.Exec(`INSERT INTO applications (user_id, key, company, role, stage, source_account, applied_at, last_activity_at, organization_id)
		VALUES ('owner', 'acme|senior pm', 'Acme', 'Senior PM', 'applied', 'me@gmail.com', ?, ?, ?)`, at0, at0, orgID)
	if err != nil {
		t.Fatal(err)
	}
	appID, _ := res.LastInsertId()

	if _, err := s.RequestResearch(ctx, "owner", "company_profile", orgID, now); err != nil {
		t.Fatal(err)
	}
	// Asking twice keeps one pending request, with the first time.
	at, err := s.RequestResearch(ctx, "owner", "company_profile", orgID, now.Add(time.Hour))
	if err != nil || !at.Equal(now) {
		t.Fatalf("at=%v err=%v", at, err)
	}
	if _, err := s.RequestResearch(ctx, "owner", "job_posting", appID, now); err != nil {
		t.Fatal(err)
	}
	// Another user's targets and unknown kinds are refused.
	if _, err := s.RequestResearch(ctx, "intruder", "job_posting", appID, now); err != ErrNotFound {
		t.Errorf("other user: %v", err)
	}
	if _, err := s.RequestResearch(ctx, "owner", "delete_everything", appID, now); err != ErrBadKind {
		t.Errorf("bad kind: %v", err)
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
	if err := s.SaveCompanyProfile(ctx, "owner", orgID, CompanyProfile{Overview: "Acme makes things.", Model: "claude-app"}, now); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.PendingResearch(ctx, "owner"); len(list) != 0 {
		t.Errorf("still pending: %+v", list)
	}
}
