package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func testQuiz(day string) Quiz {
	ans := func(i int) *int { return &i }
	q := Quiz{Day: day, Model: "claude-opus-5-5"}
	for i := 0; i < 5; i++ {
		q.Questions = append(q.Questions, QuizQuestion{Kind: "mcq", Topic: "ai_news", Prompt: "Which model was released this week?",
			Options: []string{"A", "B", "C", "D"}, Answer: ans(i % 4), Explanation: "Because B.", SourceURL: "https://example.com/news"})
	}
	q.Questions = append(q.Questions,
		QuizQuestion{Kind: "subjective", Topic: "pm_concept", Prompt: "How would you price a FinOps add-on?"},
		QuizQuestion{Kind: "voice", Topic: "org_news", Prompt: "Pitch yourself to Netomi in 60 seconds."})
	return q
}

func TestQuizValidate(t *testing.T) {
	if err := testQuiz("2026-11-17").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Quiz){
		func(q *Quiz) { q.Day = "17-11-2026" },
		func(q *Quiz) { q.Questions = q.Questions[1:] },                    // 4 MCQs
		func(q *Quiz) { q.Questions[0].Answer = nil },                      // MCQ without answer
		func(q *Quiz) { v := 4; q.Questions[0].Answer = &v },               // answer out of range
		func(q *Quiz) { q.Questions[5].Options = []string{"x", "y", "z"} }, // options on a written question
		func(q *Quiz) { q.Questions[0].Topic = "gossip" },                  // unknown topic
		func(q *Quiz) { q.Questions[0].SourceURL = "javascript:alert(1)" }, // unsafe link
		func(q *Quiz) { q.Questions[6].Kind = "subjective" },               // no voice question
	}
	for i, f := range bad {
		q := testQuiz("2026-11-17")
		f(&q)
		if q.Validate() == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestQuizFlow(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC) // 08:30 IST

	// Refreshes request today's quiz once.
	for i := 0; i < 2; i++ {
		if err := s.RequestQuiz(ctx, "u", "2026-11-17", now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if s.RequestQuiz(ctx, "u", "17/11/2026", now) == nil {
		t.Error("bad day accepted")
	}
	p, _ := s.Pending(ctx, "u", now)
	if len(p.Quizzes) != 1 || p.Quizzes[0] != "2026-11-17" {
		t.Fatalf("pending: %+v", p)
	}
	if at, _ := s.QuizQueuedAt(ctx, "u", "2026-11-17"); at == nil {
		t.Error("quiz not reported as queued")
	}

	if err := s.SaveQuiz(ctx, "u", testQuiz("2026-11-17"), now); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Pending(ctx, "u", now); len(p.Quizzes) != 0 {
		t.Errorf("quiz request not done: %+v", p)
	}
	if at, _ := s.QuizQueuedAt(ctx, "u", "2026-11-17"); at != nil {
		t.Error("still reported as queued")
	}
	// Once the quiz exists, a refresh doesn't request it again.
	if err := s.RequestQuiz(ctx, "u", "2026-11-17", now); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Pending(ctx, "u", now); len(p.Quizzes) != 0 {
		t.Errorf("re-requested an existing quiz: %+v", p)
	}
	q, err := s.QuizForDay(ctx, "u", "2026-11-17", false)
	if err != nil || len(q.Questions) != 7 {
		t.Fatalf("quiz %+v err %v", q, err)
	}
	if q.Questions[1].Answer != nil || q.Questions[1].Explanation != "" {
		t.Error("MCQ answer shown before answering")
	}
	if _, err := s.QuizForDay(ctx, "other", "2026-11-17", false); err != ErrNotFound {
		t.Errorf("other user saw the quiz: %v", err)
	}

	// MCQ: answered once, and the answer is revealed.
	mcq := q.Questions[1].ID // correct answer is 1
	one := 1
	x, err := s.AnswerQuestion(ctx, "u", mcq, &one, "", "", now)
	if err != nil || x.Response == nil || !*x.Response.Correct || x.Answer == nil || x.Explanation == "" {
		t.Fatalf("mcq answer %+v err %v", x, err)
	}
	if _, err := s.AnswerQuestion(ctx, "u", mcq, &one, "", "", now); err != ErrAnswered {
		t.Errorf("second MCQ answer: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, "other", mcq, &one, "", "", now); err != ErrNotFound {
		t.Errorf("other user answered: %v", err)
	}

	// Written answer: queues feedback; can be replaced until feedback arrives.
	sub := q.Questions[5].ID
	if _, err := s.AnswerQuestion(ctx, "u", sub, &one, "", "", now); err != ErrBadAnswer {
		t.Errorf("choice on a written question: %v", err)
	}
	for _, text := range []string{"First try", "Value-based pricing tied to savings"} {
		if _, err := s.AnswerQuestion(ctx, "u", sub, nil, text, "", now); err != nil {
			t.Fatal(err)
		}
	}
	// Voice: only a file name the server generated is accepted.
	voice := q.Questions[6].ID
	if _, err := s.AnswerQuestion(ctx, "u", voice, nil, "", "../../etc/passwd", now); err != ErrBadAnswer {
		t.Errorf("path accepted as audio file: %v", err)
	}
	if _, err := s.AnswerQuestion(ctx, "u", voice, nil, "", "q7-1700000000.webm", now); err != nil {
		t.Fatal(err)
	}
	p, _ = s.Pending(ctx, "u", now)
	if len(p.Feedback) != 2 || p.Feedback[0].QuestionID != sub || p.Feedback[0].Prompt == "" {
		t.Fatalf("pending feedback: %+v", p)
	}

	if err := s.SaveFeedback(ctx, "u", sub, "", "Good anchor on savings; add a usage tier.", 7, nil, nil, now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveFeedback(ctx, "u", mcq, "", "x", 5, nil, nil, now); err != ErrBadAnswer {
		t.Errorf("feedback on an MCQ: %v", err)
	}
	if err := s.SaveFeedback(ctx, "u", voice, "transcript", "fine", 11, nil, nil, now); err == nil {
		t.Error("score 11 accepted")
	}
	if _, err := s.AnswerQuestion(ctx, "u", sub, nil, "Changed my mind", "", now); err != ErrAnswered {
		t.Errorf("answer changed after feedback: %v", err)
	}
	if p, _ := s.Pending(ctx, "u", now); len(p.Feedback) != 1 || p.Feedback[0].QuestionID != voice {
		t.Errorf("feedback still pending: %+v", p)
	}

	// An answered quiz can't be replaced; history counts answers and correct MCQs.
	if err := s.SaveQuiz(ctx, "u", testQuiz("2026-11-17"), now); err != ErrQuizExists {
		t.Errorf("replaced an answered quiz: %v", err)
	}
	h, err := s.QuizHistory(ctx, "u", 14)
	if err != nil || len(h) != 1 || h[0].Answered != 3 || h[0].Correct != 1 || h[0].Total != 7 {
		t.Errorf("history %+v err %v", h, err)
	}
}
