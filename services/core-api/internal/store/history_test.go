package store

import (
	"testing"
	"time"
)

func TestDeriveSteps(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 8, day, 10, 0, 0, 0, time.UTC) }
	evs := []histEvent{
		{1, d(1), "applied", "Thank you for applying"},
		{2, d(10), "interview", "Technical Screening Video Conference Invite"},
		{3, d(11), "interview", "RESPONSE REQUESTED: Interview Scheduled"},
		{4, d(13), "interview", "REMINDER: Interview Scheduled on Monday"},
		{5, d(16), "interview", "Onsite In-person Interview"},
		{6, d(28), "interview", "REMINDER: Interview Scheduled on Tuesday"},
		{7, d(30), "interview", "Video Conference Invite"},
		{8, d(31), "rejected", "Update on your application"},
		{9, d(31), "update", "Keep track of your application"}, // after the decision: ignored
	}
	got := deriveSteps(evs)
	want := []struct {
		stage string
		round int
		day   int
	}{{"applied", 0, 1}, {"shortlisted", 0, 10}, {"interview", 1, 10}, {"interview", 2, 16}, {"interview", 3, 30}, {"rejected", 0, 31}}
	if len(got) != len(want) {
		t.Fatalf("got %d steps: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].stage != w.stage || got[i].round != w.round || !got[i].at.Equal(d(w.day)) {
			t.Errorf("step %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestDeriveStepsAssessmentThenOffer(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 9, day, 0, 0, 0, 0, time.UTC) }
	got := deriveSteps([]histEvent{
		{1, d(1), "applied", ""}, {2, d(4), "assessment", "HackerRank invite"}, {3, d(9), "interview", "Interview invite"}, {4, d(20), "offer", "Your offer"},
	})
	stages := ""
	for _, s := range got {
		stages += s.stage + ","
	}
	if stages != "applied,shortlisted,assessment,interview,offer," {
		t.Errorf("got %s", stages)
	}
}

func TestNewCycleAfterDecision(t *testing.T) {
	d := func(day int) time.Time { return time.Date(2026, 8, day, 0, 0, 0, 0, time.UTC) }
	got := deriveSteps([]histEvent{
		{1, d(1), "applied", ""}, {2, d(3), "rejected", ""}, {3, d(10), "applied", "Thank you for applying"}, {4, d(12), "update", ""},
	})
	if len(got) != 1 || got[0].stage != "applied" || !got[0].at.Equal(d(10)) {
		t.Errorf("want only the latest cycle, got %+v", got)
	}
}

func TestNextMove(t *testing.T) {
	cases := []struct {
		stage string
		days  int
		title string
	}{
		{"applied", 3, "Wait"}, {"applied", 15, "Send a follow-up"}, {"applied", 30, "Find a referral"}, {"applied", 60, "Consider it closed"},
		{"assessment", 2, "Complete the assessment"}, {"interview", 1, "Send thank-you notes"}, {"interview", 20, "Ask for feedback"},
		{"offer", 1, "Decide on the offer"},
	}
	for _, c := range cases {
		a := AppAnalysis{Application: Application{Stage: c.stage}, DaysInStage: c.days}
		if got := nextMove(a, 10).Title; got != c.title {
			t.Errorf("%s/%d: got %q, want %q", c.stage, c.days, got, c.title)
		}
	}
}
