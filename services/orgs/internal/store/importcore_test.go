package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// coreDB makes a database shaped like core-api's before its migration v14.
func coreDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dzinga.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ddl := migrations[0][:strings.Index(migrations[0], "-- Company research queued")]
	ddl += `CREATE TABLE research_requests (id INTEGER PRIMARY KEY, user_id TEXT NOT NULL, kind TEXT NOT NULL,
		target_id INTEGER NOT NULL, status TEXT NOT NULL DEFAULT 'pending', created_at TEXT NOT NULL, done_at TEXT);`
	for _, q := range []string{ddl,
		`INSERT INTO organizations VALUES (7, 'owner', 'acme', 'Acme', '2026-10-01T00:00:00Z'), (9, 'owner', 'beta', 'Beta', '2026-10-01T00:00:00Z')`,
		`INSERT INTO org_settings VALUES ('owner', 'manual')`,
		`INSERT INTO org_preferences VALUES ('owner', 9, 0)`,
		`INSERT INTO company_profiles VALUES (7, 'owner', 'Acme makes rockets.', 'Growth.', 'claude-opus-5-5', '2026-10-02T00:00:00Z')`,
		`INSERT INTO company_news (organization_id, position, title) VALUES (7, 0, 'Acme raises')`,
		`INSERT INTO org_briefings (organization_id, user_id, ceo, model, updated_at) VALUES (9, 'owner', 'Jane Doe', 'claude-opus-5-5', '2026-10-03T00:00:00Z')`,
		`INSERT INTO research_requests (user_id, kind, target_id, created_at) VALUES
			('owner', 'company_profile', 9, '2026-10-04T00:00:00Z'), ('owner', 'fit_score', 9, '2026-10-04T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestImportFromCore(t *testing.T) {
	src := coreDB(t)
	s, err := Open(filepath.Join(t.TempDir(), "orgs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	n, err := s.ImportFromCore(ctx, src)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Ids, preferences, profiles, briefings and pending company research all carried over.
	apps := []App{{ID: 1, Company: "ACME", Stage: "applied", Open: true}}
	rep, err := s.Organizations(ctx, "owner", apps, time.Now())
	if err != nil || rep.PreferredMode != "manual" || len(rep.Preferred) != 1 || rep.Preferred[0].ID != 9 {
		t.Fatalf("report: %+v err=%v", rep, err)
	}
	if id, _ := s.OrgID(ctx, "owner", "Acme"); id != 7 {
		t.Errorf("acme id %d", id)
	}
	d, err := s.Organization(ctx, "owner", 7, apps, time.Now())
	if err != nil || d.Profile == nil || len(d.Profile.News) != 1 || len(d.History) != 1 {
		t.Errorf("acme detail: %+v err=%v", d, err)
	}
	if d, _ := s.Organization(ctx, "owner", 9, apps, time.Now()); d.Briefing == nil || d.Briefing.CEO != "Jane Doe" || d.ResearchRequestedAt == nil {
		t.Errorf("beta detail: %+v", d)
	}
	if q, _ := s.PendingResearch(ctx, "owner"); len(q) != 1 || q[0].TargetID != 9 {
		t.Errorf("queue: %+v", q)
	}
	// Only once.
	if _, err := s.ImportFromCore(ctx, src); err == nil {
		t.Error("a second import was allowed")
	}
}
