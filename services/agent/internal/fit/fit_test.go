package fit

import (
	"strings"
	"testing"

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
