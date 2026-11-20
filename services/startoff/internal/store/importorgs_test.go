package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// orgsV2 is the part of the organizations service's schema (at its v2) the import reads.
const orgsV2 = `
CREATE TABLE organizations (id INTEGER PRIMARY KEY, user_id TEXT, key TEXT, name TEXT, created_at TEXT);
CREATE TABLE company_profiles (organization_id INTEGER PRIMARY KEY, user_id TEXT, overview TEXT, interview_angle TEXT, model TEXT, created_at TEXT);
CREATE TABLE company_news (id INTEGER PRIMARY KEY, organization_id INTEGER, position INTEGER, title TEXT, published_on TEXT, summary TEXT, url TEXT);
CREATE TABLE company_products (id INTEGER PRIMARY KEY, organization_id INTEGER, position INTEGER, name TEXT, description TEXT);
CREATE TABLE company_leaders (id INTEGER PRIMARY KEY, organization_id INTEGER, position INTEGER, name TEXT, title TEXT);
CREATE TABLE company_ratings (id INTEGER PRIMARY KEY, organization_id INTEGER, source TEXT, score REAL, scale REAL, review_count INTEGER, url TEXT, as_of TEXT);
CREATE TABLE org_diagnoses (id INTEGER PRIMARY KEY, user_id TEXT, organization_id INTEGER, summary TEXT, model TEXT, created_at TEXT, seen_at TEXT);
CREATE TABLE org_diagnosis_items (id INTEGER PRIMARY KEY, diagnosis_id INTEGER, kind TEXT, position INTEGER, text TEXT, confidence TEXT);
CREATE TABLE org_briefings (organization_id INTEGER PRIMARY KEY, user_id TEXT, ceo TEXT, cpo TEXT, revenue_year TEXT, revenue_year_period TEXT,
	revenue_year_url TEXT, revenue_quarter TEXT, revenue_quarter_period TEXT, revenue_quarter_url TEXT, leaders_url TEXT, model TEXT, updated_at TEXT, seen_at TEXT);
CREATE TABLE org_briefing_news (id INTEGER PRIMARY KEY, organization_id INTEGER, day TEXT, title TEXT, summary TEXT, source_title TEXT, url TEXT, published_on TEXT);
CREATE TABLE research_requests (id INTEGER PRIMARY KEY, user_id TEXT, organization_id INTEGER, status TEXT, created_at TEXT, done_at TEXT, kind TEXT);

INSERT INTO organizations VALUES (7, 'owner', 'acme', 'Acme', '2026-10-01T00:00:00Z'), (9, 'owner', 'globex', 'Globex', '2026-10-01T00:00:00Z');
INSERT INTO company_profiles VALUES (7, 'owner', 'Acme makes rockets.', 'Lead with growth.', 'claude-opus-5-5', '2026-10-10T00:00:00Z');
INSERT INTO company_news VALUES (1, 7, 0, 'Acme raises', '2026-09-01', '', 'https://news.example/acme');
INSERT INTO company_leaders VALUES (1, 7, 0, 'Wile E.', 'CEO');
INSERT INTO company_ratings VALUES (1, 7, 'glassdoor', 3.9, 5, 1200, 'https://www.glassdoor.com/acme', '');
INSERT INTO org_briefings VALUES (7, 'owner', 'Wile E.', '', '$1B', 'FY2025', 'https://ir.example/fy', '', '', '', '', 'claude-opus-5-5', '2026-11-19T00:00:00Z', NULL);
INSERT INTO org_briefing_news VALUES (1, 7, '2026-11-19', 'Acme buys back stock', '', 'Reuters', 'https://news.example/a', '2026-11-18');
INSERT INTO org_diagnoses VALUES (3, 'owner', 9, 'Silent so far.', 'claude-opus-5-5', '2026-11-20T02:00:00Z', '2026-11-20T03:00:00Z');
INSERT INTO org_diagnosis_items VALUES (1, 3, 'cause', 0, 'Timing', 'medium'), (2, 3, 'action', 0, 'Wait', '');
INSERT INTO research_requests VALUES (1, 'owner', 9, 'pending', '2026-11-20T02:00:00Z', NULL, 'company_profile');
PRAGMA user_version = 2;
`

func TestImportFromOrgs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "orgs.db")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(orgsV2); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(filepath.Join(dir, "startoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	n, err := s.ImportFromOrgs(ctx, src)
	if err != nil || n != 2 {
		t.Fatalf("import: %d, %v", n, err)
	}
	acme, err := s.OrgResearch(ctx, "owner", 7)
	if err != nil {
		t.Fatal(err)
	}
	if acme.Profile == nil || acme.Profile.Leaders[0].Name != "Wile E." || len(acme.Profile.News) != 1 || *acme.Profile.Ratings[0].Score != 3.9 {
		t.Errorf("profile: %+v", acme.Profile)
	}
	if acme.Briefing == nil || acme.Briefing.RevenueYear.Value != "$1B" || len(acme.Briefing.News) != 1 || !acme.BriefingUnseen {
		t.Errorf("briefing: %+v unseen=%v", acme.Briefing, acme.BriefingUnseen)
	}
	globex, _ := s.OrgResearch(ctx, "owner", 9)
	if globex.Diagnosis == nil || globex.Diagnosis.Causes[0].Text != "Timing" || globex.DiagnosisUnseen || globex.ResearchRequestedAt == nil {
		t.Errorf("globex: %+v", globex)
	}
	// New saves continue the history.
	if err := s.SaveDiagnosis(ctx, "owner", Org{ID: 9, Name: "Globex"}, Diagnosis{Summary: "Moving now.", Model: "m"}, time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.OrgResearch(ctx, "owner", 9); len(g.EarlierDiagnoses) != 1 || !g.DiagnosisUnseen {
		t.Errorf("after a new diagnosis: %+v", g)
	}
	if _, err := s.ImportFromOrgs(ctx, src); err == nil || !strings.Contains(err.Error(), "only runs once") {
		t.Errorf("second import: %v", err)
	}
}

func TestImportRefusesAnOrgsDatabaseAfterV3(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "orgs.db")
	db, _ := sql.Open("sqlite", src)
	db.Exec(`CREATE TABLE organizations (id INTEGER PRIMARY KEY); PRAGMA user_version = 3;`)
	db.Close()
	s, err := Open(filepath.Join(dir, "startoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ImportFromOrgs(context.Background(), src); err == nil || !strings.Contains(err.Error(), "use a backup") {
		t.Errorf("import from v3: %v", err)
	}
}
