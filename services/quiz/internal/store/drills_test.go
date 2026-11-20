package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDrillsAreSetThenCheckedTheNextDay(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "quiz.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, IST)
	written := map[string]int64{}
	voice := map[string]int64{}
	for _, day := range []string{"2026-11-20", "2026-11-21"} {
		if err := s.SaveQuiz(ctx, "u", testQuiz(day), now); err != nil {
			t.Fatal(err)
		}
		q, _ := s.QuizForDay(ctx, "u", day, true)
		written[day], voice[day] = q.Questions[5].ID, q.Questions[6].ID
		if _, err := s.AnswerQuestion(ctx, "u", written[day], nil, "My answer", "", now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AnswerQuestion(ctx, "u", voice[day], nil, "", "q7-1.webm", now); err != nil {
			t.Fatal(err)
		}
	}

	// Day 1: feedback sets drills.
	err = s.SaveFeedback(ctx, "u", written["2026-11-20"], "", "Pick one north star.", 5, []NewDrill{
		{Kind: "subjective", Text: "Name one north-star metric, not four"},
		{Kind: "any", Text: "Tie the answer to the CEO's framing"},
	}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFeedback(ctx, "u", voice["2026-11-20"], "hi", "Fewer hedges.", 5,
		[]NewDrill{{Kind: "voice", Text: "No 'I believe'; open with the customer problem"}}, nil, now); err != nil {
		t.Fatal(err)
	}
	open, _ := s.OpenDrills(ctx, "u", "subjective", "2026-11-21")
	if len(open) != 2 {
		t.Fatalf("open drills for a written answer: %+v", open)
	}
	// A drill can't be checked against an answer from the same day.
	if err := s.SaveFeedback(ctx, "u", written["2026-11-20"], "", "again", 5, nil,
		[]DrillCheck{{DrillID: open[0].ID, Result: "met"}}, now); !errors.Is(err, ErrBadDrill) {
		t.Errorf("same-day check: %v", err)
	}
	// Nor a voice drill against a written answer.
	all, _ := s.OpenDrills(ctx, "u", "", "")
	var voiceDrill int64
	for _, d := range all {
		if d.Kind == "voice" {
			voiceDrill = d.ID
		}
	}
	if err := s.SaveFeedback(ctx, "u", written["2026-11-21"], "", "x", 6, nil,
		[]DrillCheck{{DrillID: voiceDrill, Result: "met"}}, now); !errors.Is(err, ErrBadDrill) {
		t.Errorf("voice drill on a written answer: %v", err)
	}

	// Day 2: the written answer meets one drill and partly meets the other.
	if err := s.SaveFeedback(ctx, "u", written["2026-11-21"], "", "Better.", 7, nil, []DrillCheck{
		{DrillID: open[0].ID, Result: "met", Note: "One north star: renewal rate."},
		{DrillID: open[1].ID, Result: "partly"},
	}, now); err != nil {
		t.Fatal(err)
	}
	ft, n, err := s.FollowThrough(ctx, "u", "2026-11-15")
	if err != nil || n != 2 || ft == nil || *ft != 75 {
		t.Errorf("follow-through %v of %d, err %v", ft, n, err)
	}
	if open, _ := s.OpenDrills(ctx, "u", "", ""); len(open) != 1 || open[0].Kind != "voice" {
		t.Errorf("still open: %+v", open)
	}
	if recent, _ := s.RecentChecks(ctx, "u", "2026-11-14"); len(recent) != 2 || recent[0].CheckedDay != "2026-11-21" {
		t.Errorf("recent checks: %+v", recent)
	}
	st, _ := s.Stats(ctx, "u", now)
	if st.FollowThrough7d == nil || *st.FollowThrough7d != 75 || st.DrillsChecked7d != 2 || st.DrillsOpen != 1 {
		t.Errorf("stats: %+v", st)
	}
	if err := s.SaveFeedback(ctx, "u", voice["2026-11-21"], "", "x", 5, []NewDrill{{Kind: "both", Text: "not a valid kind here"}}, nil, now); !errors.Is(err, ErrBadDrill) {
		t.Errorf("bad kind: %v", err)
	}
}
