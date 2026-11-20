package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testNews(day string) News {
	n := News{Day: day}
	for _, c := range []string{"", "Acme", "Beta"} {
		x := NewsItem{Topic: "ai_news", Title: "A model was released", Summary: "It is faster at agent tasks.",
			SourceTitle: "Lab blog", URL: "https://example.com/post", PublishedOn: "2026-11-12"}
		if c != "" {
			x.Topic, x.Company = "org_news", c
		}
		n.Items = append(n.Items, x)
	}
	return n
}

func TestNews(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)

	if n, _ := s.LatestNews(ctx, "u"); n.Day != "" || len(n.Items) != 0 {
		t.Errorf("empty feed: %+v", n)
	}
	bad := []func(*News){
		func(n *News) { n.Items = n.Items[:2] },
		func(n *News) { n.Items[0].URL = "http://example.com" },
		func(n *News) { n.Items[1].Company = "" },
		func(n *News) { n.Items[0].Topic = "gossip" },
		func(n *News) { n.Items[0].PublishedOn = "last week" },
	}
	for i, f := range bad {
		n := testNews("2026-11-17")
		f(&n)
		if s.SaveNews(ctx, "u", n) == nil {
			t.Errorf("case %d accepted", i)
		}
	}

	// A requested day without news is pending; saving clears it, and the newest day wins.
	s.RequestQuiz(ctx, "u", "2026-11-17", now)
	if p, _ := s.Pending(ctx, "u", now); len(p.News) != 1 {
		t.Fatalf("pending news: %+v", p)
	}
	for _, d := range []string{"2026-11-16", "2026-11-17"} {
		if err := s.SaveNews(ctx, "u", testNews(d)); err != nil {
			t.Fatal(err)
		}
	}
	if p, _ := s.Pending(ctx, "u", now); len(p.News) != 0 {
		t.Errorf("still pending: %+v", p)
	}
	n, err := s.LatestNews(ctx, "u")
	if err != nil || n.Day != "2026-11-17" || len(n.Items) != 3 || n.Items[1].Company != "Acme" {
		t.Errorf("latest %+v err %v", n, err)
	}
	if n, _ := s.LatestNews(ctx, "other"); len(n.Items) != 0 {
		t.Error("another user saw the news")
	}
	// Old requests stop asking for news after a few days.
	if p, _ := s.Pending(ctx, "u", now.AddDate(0, 0, 5)); len(p.News) != 0 {
		t.Errorf("stale news request: %+v", p)
	}
}

func TestStats(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 6, 0, 0, 0, time.UTC)

	st, err := s.Stats(ctx, "u", now)
	if err != nil || st.Accuracy != nil || st.Streak != 0 || st.QuizzesTaken != 0 {
		t.Fatalf("empty stats %+v err %v", st, err)
	}
	// Yesterday: 2 of 2 right. Today: 1 of 2 right, plus a written answer scored 7.
	answer := func(day string, picks []int, written bool) {
		if err := s.SaveQuiz(ctx, "u", testQuiz(day), now); err != nil {
			t.Fatal(err)
		}
		q, _ := s.QuizForDay(ctx, "u", day, true)
		for i, c := range picks {
			c := c
			if _, err := s.AnswerQuestion(ctx, "u", q.Questions[i].ID, &c, "", "", now); err != nil {
				t.Fatal(err)
			}
		}
		if written {
			s.AnswerQuestion(ctx, "u", q.Questions[5].ID, nil, "Tiered pricing", "", now)
			s.SaveFeedback(ctx, "u", q.Questions[5].ID, "", "Good.", 7, nil, nil, now)
			s.AnswerQuestion(ctx, "u", q.Questions[6].ID, nil, "", "q7-1.webm", now)
		}
	}
	answer("2026-11-16", []int{0, 1}, false) // testQuiz answers are i%4: both right
	answer("2026-11-17", []int{0, 0}, true)  // second is wrong
	st, err = s.Stats(ctx, "u", now)
	if err != nil {
		t.Fatal(err)
	}
	if st.QuizzesTaken != 2 || st.MCQAnswered != 4 || st.MCQCorrect != 3 || *st.Accuracy != 75 || *st.Accuracy7d != 75 {
		t.Errorf("accuracy %+v", st)
	}
	if st.WrittenAvg == nil || *st.WrittenAvg != 7 || st.SpokenAvg != nil || st.FeedbackQueued != 1 {
		t.Errorf("scores %+v", st)
	}
	if st.Streak != 2 || len(st.Days) != 2 || st.LastAnsweredAt == nil {
		t.Errorf("streak %d days %+v", st.Streak, st.Days)
	}
	// Tomorrow morning, before answering, the streak still counts.
	if st, _ := s.Stats(ctx, "u", now.AddDate(0, 0, 1)); st.Streak != 2 {
		t.Errorf("next-morning streak %d", st.Streak)
	}
	if st, _ := s.Stats(ctx, "u", now.AddDate(0, 0, 3)); st.Streak != 0 {
		t.Errorf("broken streak %d", st.Streak)
	}
}
