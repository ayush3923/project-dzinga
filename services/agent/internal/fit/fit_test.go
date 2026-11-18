package fit

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/agent/internal/core"
)

func profile() core.Profile {
	return core.Profile{CurrentTitle: "Senior Product Manager – Growth Products", TotalYears: 14, Location: "Bengaluru, India",
		Skills: []string{"Product strategy", "Pricing", "AWS", "FinOps"},
		Experiences: []core.Experience{{Title: "Senior Product Manager – Growth Products", Company: "New Relic", Highlights: []string{
			"Built a new FinOps product from concept to commercialization; positioned to generate $600K ARR.",
			"Ran pricing and product experiments with enterprise design partners: 100% conversion into GA.",
			"Partnered with Data Science and Engineering on AI-enabled customer experiences.",
		}}}}
}

func intent() core.Intent {
	return core.Intent{Roles: []string{"Principal Product Manager", "Senior Product Manager", "Solutions Architect"},
		Designation: "Senior", Locations: []string{"Bengaluru", "Mumbai", "Remote (India)"}}
}

func years(n int) *int { return &n }

func TestStrongMatch(t *testing.T) {
	post := core.Posting{Title: "Principal Product Manager, Cloud Cost", Location: "Bangalore, India", WorkMode: "hybrid", MinYears: years(10),
		Requirements:     []string{"10+ years of product management experience", "Experience with B2B SaaS pricing and experimentation", "Background in AWS or multi-cloud cost management"},
		Responsibilities: []string{"Own the FinOps roadmap and launch new AI features"}}
	r := Score(profile(), intent(), post)
	if r.Total < 75 {
		t.Fatalf("expected a strong score, got %d: %+v", r.Total, r.Factors)
	}
	byName := map[string]int{}
	for _, f := range r.Factors {
		byName[f.Factor] = f.Score
	}
	if byName["location"] != 20 || byName["seniority"] != 20 {
		t.Errorf("location/seniority: %+v", r.Factors)
	}
	if len(r.Strengths) == 0 {
		t.Error("no strengths")
	}
}

func TestWeakMatch(t *testing.T) {
	post := core.Posting{Title: "Associate Data Analyst", Location: "London, UK", WorkMode: "onsite", MinYears: years(2),
		Requirements: []string{"SQL and Tableau dashboards", "Statistics degree", "Excel modelling"}}
	r := Score(profile(), intent(), post)
	if r.Total > 40 {
		t.Fatalf("expected a weak score, got %d: %+v", r.Total, r.Factors)
	}
	if len(r.Gaps) == 0 {
		t.Error("no gaps")
	}
	v, s := Fallback(r, post)
	if !strings.Contains(v, "weak fit") || len(s) == 0 {
		t.Errorf("fallback: %q %v", v, s)
	}
}

func TestDeterministic(t *testing.T) {
	post := core.Posting{Title: "Senior Product Manager", Location: "Pune", Requirements: []string{"Experience with AI platforms", "Strong stakeholder management"}}
	a, b := Score(profile(), intent(), post), Score(profile(), intent(), post)
	if a.Total != b.Total || len(a.Evidence) != len(b.Evidence) {
		t.Fatal("same inputs gave different scores")
	}
}

func TestLevelsAndFunctions(t *testing.T) {
	cases := map[string]int{"Product Manager": 2, "Senior Product Manager": 3, "Lead PM": 4, "Product Manager 4": 4,
		"Principal Product Manager": 5, "Group Product Manager": 5, "Director of Product": 6, "VP Product": 7, "Associate Product Manager": 1}
	for title, want := range cases {
		if got := Level(title); got != want {
			t.Errorf("Level(%q) = %d, want %d", title, got, want)
		}
	}
	if Function("Solutions Architect") != "solutions" || Function("Senior Product Manager") != "product" {
		t.Error("functions")
	}
}

func TestSynonyms(t *testing.T) {
	a, b := Tokens("Experience with GenAI and LLMs on AWS"), Tokens("machine learning on Azure")
	if !a["ai"] || !b["ai"] || !a["cloud"] || !b["cloud"] {
		t.Errorf("%v %v", a, b)
	}
}

func TestBareTargetRoleDoesNotWidenBand(t *testing.T) {
	post := core.Posting{Title: "Product Manager", Location: "Bengaluru", Requirements: []string{"Pricing experiments"}}
	r := Score(profile(), intent(), post)
	for _, f := range r.Factors {
		if f.Factor == "seniority" && (f.Score != 12 || !strings.Contains(f.Rationale, "senior to principal")) {
			t.Errorf("seniority: %+v", f)
		}
	}
}

// datedProfile has role dates: 11 years in product roles (one of them, 2 years at
// New Relic, in observability), 14 in total.
func datedProfile() core.Profile {
	p := core.Profile{CurrentTitle: "Senior Product Manager", TotalYears: 14, Location: "Bengaluru, India",
		Summary: "Product leader across cloud, observability, AI, payments, platforms and enterprise SaaS growth.",
		Experiences: []core.Experience{
			{Title: "Senior Product Manager", Company: "New Relic", StartMonth: "2024-09",
				Highlights: []string{"Launched cost insights inside the observability platform for enterprise customers"}},
			{Title: "Senior Product Manager", Company: "American Express", StartMonth: "2021-10", EndMonth: "2024-08",
				Highlights: []string{"Moved card payments services to the cloud"}},
			{Title: "Product Manager", Company: "Tata Communications", StartMonth: "2015-09", EndMonth: "2021-09"},
			{Title: "BPM Developer", Company: "Cognizant", StartMonth: "2011-07", EndMonth: "2014-06"},
		}}
	return p
}

func pinNow(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
}

func TestYearsRequirements(t *testing.T) {
	pinNow(t)
	p := datedProfile()
	cases := []struct{ req, strength, evidence string }{
		// The domain figure counts only the observability role (New Relic, 2 years).
		{"10+ years of product management, including 7+ years owning observability or monitoring capabilities", "partial", "about 2 years in this area (New Relic), short of 7"},
		{"8+ years of product management, including 2+ years in observability", "strong", "about 2 years in this area (New Relic)"},
		{"10+ years of product management, including 5+ years in healthcare", "missing", ""},
		// Product years (11) are held against a product-management bar, not total years (14).
		{"12+ years of product management experience", "partial", "11 years in product roles, short of 12"},
		{"12+ years of experience in technology", "strong", "14 years of total experience"},
	}
	for _, c := range cases {
		ev := Correlate(p, []string{c.req})[0]
		if ev.Strength != c.strength || !strings.Contains(ev.Evidence, c.evidence) {
			t.Errorf("%q: got %s %q, want %s containing %q", c.req, ev.Strength, ev.Evidence, c.strength, c.evidence)
		}
	}
}

func TestProductYearsNeedDates(t *testing.T) {
	// Without role dates, a product-management bar falls back to total years.
	ev := Correlate(profile(), []string{"12+ years of product management experience"})[0]
	if ev.Strength != "strong" || !strings.Contains(ev.Evidence, "total experience") {
		t.Errorf("got %s %q", ev.Strength, ev.Evidence)
	}
}

func TestSummaryOnlyMatchesAreLimited(t *testing.T) {
	pinNow(t)
	p := datedProfile()
	// One shared word with the summary is no longer evidence at all.
	if ev := Correlate(p, []string{"Deep knowledge of serverless payments reconciliation and ledgers"})[0]; ev.Strength != "missing" {
		t.Errorf("one summary word: got %s %q", ev.Strength, ev.Evidence)
	}
	// Two shared words still make a partial, but it weighs half a role-backed partial.
	ev := Correlate(p, []string{"Shaped AI roadmaps for SaaS products"})
	if ev[0].Strength != "partial" || !strings.HasPrefix(ev[0].Evidence, "Summary: ") {
		t.Fatalf("two summary words: got %s %q", ev[0].Strength, ev[0].Evidence)
	}
	if c := coverage(ev); c != 0.25 {
		t.Errorf("summary-only partial coverage = %v, want 0.25", c)
	}
}

func TestBandsMatchRunbook(t *testing.T) {
	for total, want := range map[int]string{80: "strong", 79: "good", 75: "good", 74: "moderate", 60: "moderate", 59: "weak"} {
		if got := Band(total); got != want {
			t.Errorf("Band(%d) = %q, want %q", total, got, want)
		}
	}
	// The runbook Claude follows for Opus scores must state the same scale.
	doc, err := os.ReadFile("../../../../docs/runbooks/claude-queue.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), Bands) {
		t.Errorf("docs/runbooks/claude-queue.md doesn't state the bands %q", Bands)
	}
	r := Result{Total: 69, Factors: []core.FitFactor{{Factor: "location", Score: 6}}}
	if v, _ := Fallback(r, core.Posting{Title: "PM"}); !strings.Contains(v, "a moderate fit") {
		t.Errorf("fallback verdict %q", v)
	}
}

func TestSynonymsMatchWholeWords(t *testing.T) {
	for _, s := range []string{"knowledge of ledgers", "scaled and enabled", "division supervision", "therapist"} {
		if tok := Tokens(s); tok["lead"] || tok["strategy"] || tok["api"] {
			t.Errorf("Tokens(%q) = %v: a synonym matched inside a word", s, tok)
		}
	}
	if tok := Tokens("Team leader who led and mentored; product vision"); !tok["lead"] || !tok["strategy"] {
		t.Errorf("whole words lost: %v", Tokens("Team leader who led and mentored; product vision"))
	}
}
