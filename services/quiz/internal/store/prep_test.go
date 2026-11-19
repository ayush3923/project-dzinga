package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testPrep(day string) Prep {
	p := Prep{Day: day}
	for _, c := range []string{"Acme", "Beta", ""} {
		p.Items = append(p.Items, PrepQuestion{Company: c, Role: "Senior PM",
			Question: "Walk me through a launch that missed its goal.", Why: "They probe judgement after round one.",
			Approach: "Situation in one line, the metric you missed, what you changed, and the result after."})
	}
	return p
}

func TestPrep(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)

	if p, _ := s.LatestPrep(ctx, "u", "2026-11-19"); p.Day != "" || len(p.Items) != 0 {
		t.Errorf("empty: %+v", p)
	}
	bad := []func(*Prep){
		func(p *Prep) { p.Items = p.Items[:2] },
		func(p *Prep) { p.Day = "today" },
		func(p *Prep) { p.Items[0].Question = "Why?" },
		func(p *Prep) { p.Items[1].Approach = "" },
	}
	for i, f := range bad {
		p := testPrep("2026-11-19")
		f(&p)
		if s.SavePrep(ctx, "u", p) == nil {
			t.Errorf("case %d accepted", i)
		}
	}

	// Today without prep is pending (yesterday's is no use); saving clears it.
	s.RequestQuiz(ctx, "u", "2026-11-18", now)
	s.RequestQuiz(ctx, "u", "2026-11-19", now)
	if p, _ := s.Pending(ctx, "u", now); len(p.Prep) != 1 || p.Prep[0] != "2026-11-19" {
		t.Fatalf("pending prep: %+v", p.Prep)
	}
	for _, d := range []string{"2026-11-18", "2026-11-19"} {
		if err := s.SavePrep(ctx, "u", testPrep(d)); err != nil {
			t.Fatal(err)
		}
	}
	if p, _ := s.Pending(ctx, "u", now); len(p.Prep) != 0 {
		t.Errorf("still pending: %+v", p.Prep)
	}
	// The newest day on or before the one asked for; another user sees nothing.
	if p, _ := s.LatestPrep(ctx, "u", "2026-11-18"); p.Day != "2026-11-18" || len(p.Items) != 3 || p.Items[2].Company != "" {
		t.Errorf("latest: %+v", p)
	}
	if p, _ := s.LatestPrep(ctx, "other", "2026-11-19"); len(p.Items) != 0 {
		t.Errorf("other user: %+v", p)
	}
}
