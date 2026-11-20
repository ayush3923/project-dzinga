package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Key news: the AI and company news Claude reads while building each day's quiz,
// kept as a short feed for the profile page. It refreshes with the quiz.

type NewsItem struct {
	Topic       string `json:"topic"`   // ai_news | org_news
	Company     string `json:"company"` // org_news only
	Title       string `json:"title"`
	Summary     string `json:"summary"`
	SourceTitle string `json:"source_title"`
	URL         string `json:"url"`
	PublishedOn string `json:"published_on"`
}

type News struct {
	Day   string     `json:"day"`
	Items []NewsItem `json:"items"`
}

func (n News) Validate() error {
	if !reDay.MatchString(n.Day) {
		return errors.New("day must be YYYY-MM-DD")
	}
	if len(n.Items) < 3 || len(n.Items) > 12 {
		return errors.New("send 3-12 news items")
	}
	for i, x := range n.Items {
		switch {
		case x.Topic != "ai_news" && x.Topic != "org_news":
			return fmt.Errorf("item %d: topic must be ai_news or org_news", i+1)
		case x.Topic == "org_news" && strings.TrimSpace(x.Company) == "":
			return fmt.Errorf("item %d: company news needs the company", i+1)
		case len([]rune(strings.TrimSpace(x.Title))) < 5 || len([]rune(x.Title)) > 160:
			return fmt.Errorf("item %d: title must be 5-160 characters", i+1)
		case len([]rune(strings.TrimSpace(x.Summary))) < 10 || len([]rune(x.Summary)) > 400:
			return fmt.Errorf("item %d: summary must be 10-400 characters", i+1)
		case !strings.HasPrefix(x.URL, "https://") || len(x.URL) > 2000:
			return fmt.Errorf("item %d: a https source link is required", i+1)
		case strings.TrimSpace(x.SourceTitle) == "" || len([]rune(x.SourceTitle)) > 120:
			return fmt.Errorf("item %d: source_title must be 1-120 characters", i+1)
		case x.PublishedOn != "" && !reDay.MatchString(x.PublishedOn):
			return fmt.Errorf("item %d: published_on must be YYYY-MM-DD", i+1)
		}
	}
	return nil
}

// SaveNews replaces a day's news.
func (s *Store) SaveNews(ctx context.Context, userID string, n News) error {
	if err := n.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM news_items WHERE user_id = ? AND day = ?`, userID, n.Day); err != nil {
		return err
	}
	for i, x := range n.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO news_items (user_id, day, position, topic, company, title, summary, source_title, url, published_on)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, userID, n.Day, i, x.Topic, strings.TrimSpace(x.Company), strings.TrimSpace(x.Title),
			strings.TrimSpace(x.Summary), strings.TrimSpace(x.SourceTitle), x.URL, x.PublishedOn); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LatestNews returns the most recent day's news (an empty feed when there is none).
func (s *Store) LatestNews(ctx context.Context, userID string) (News, error) {
	n := News{Items: []NewsItem{}}
	err := s.db.QueryRowContext(ctx, `SELECT day FROM news_items WHERE user_id = ? ORDER BY day DESC LIMIT 1`, userID).Scan(&n.Day)
	if errors.Is(err, sql.ErrNoRows) {
		return n, nil
	}
	if err != nil {
		return n, err
	}
	err = s.queryRows(ctx, `SELECT topic, company, title, summary, source_title, url, published_on FROM news_items
		WHERE user_id = ? AND day = ? ORDER BY position`, []any{userID, n.Day}, func(r *sql.Rows) error {
		var x NewsItem
		err := r.Scan(&x.Topic, &x.Company, &x.Title, &x.Summary, &x.SourceTitle, &x.URL, &x.PublishedOn)
		n.Items = append(n.Items, x)
		return err
	})
	return n, err
}

// Stats summarise quiz performance for the profile page.
type Stats struct {
	QuizzesTaken   int        `json:"quizzes_taken"` // days with at least one answer
	MCQAnswered    int        `json:"mcq_answered"`
	MCQCorrect     int        `json:"mcq_correct"`
	Accuracy       *int       `json:"accuracy"`        // % of MCQs right, all time
	Accuracy7d     *int       `json:"accuracy_7d"`     // % right over the last 7 days
	WrittenAvg     *float64   `json:"written_avg"`     // feedback score out of 10
	SpokenAvg      *float64   `json:"spoken_avg"`      // feedback score out of 10
	Streak         int        `json:"streak"`          // consecutive days answered, ending today or yesterday
	FeedbackQueued int        `json:"feedback_queued"` // answers awaiting feedback
	Days           []QuizDay  `json:"days"`            // last 14 quizzes, newest first
	LastAnsweredAt *time.Time `json:"last_answered_at"`
	// Improvement drills checked in the last 7 days, and how well they were followed
	// (met = 1, partly = ½), 0-100; nil when none were checked.
	DrillsChecked7d int  `json:"drills_checked_7d"`
	FollowThrough7d *int `json:"follow_through_7d"`
	DrillsOpen      int  `json:"drills_open"`
}

func pct(n, d int) *int {
	if d == 0 {
		return nil
	}
	v := (n*100 + d/2) / d
	return &v
}

func (s *Store) Stats(ctx context.Context, userID string, now time.Time) (Stats, error) {
	st := Stats{Days: []QuizDay{}}
	today := now.In(IST)
	weekAgo := today.AddDate(0, 0, -6).Format("2006-01-02")
	var c7, n7 int
	var written, spoken sql.NullFloat64
	var last sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT
			COUNT(DISTINCT CASE WHEN qa.question_id IS NOT NULL THEN z.id END),
			COALESCE(SUM(qq.kind = 'mcq' AND qa.question_id IS NOT NULL), 0),
			COALESCE(SUM(qq.kind = 'mcq' AND qa.choice = qq.answer), 0),
			COALESCE(SUM(qq.kind = 'mcq' AND qa.question_id IS NOT NULL AND z.day >= ?), 0),
			COALESCE(SUM(qq.kind = 'mcq' AND qa.choice = qq.answer AND z.day >= ?), 0),
			AVG(CASE WHEN qq.kind = 'subjective' THEN qa.score END),
			AVG(CASE WHEN qq.kind = 'voice' THEN qa.score END),
			COALESCE(SUM(qq.kind <> 'mcq' AND qa.question_id IS NOT NULL AND qa.feedback_at IS NULL), 0),
			MAX(qa.answered_at)
		FROM quizzes z JOIN quiz_questions qq ON qq.quiz_id = z.id LEFT JOIN quiz_answers qa ON qa.question_id = qq.id
		WHERE z.user_id = ?`, weekAgo, weekAgo, userID).
		Scan(&st.QuizzesTaken, &st.MCQAnswered, &st.MCQCorrect, &n7, &c7, &written, &spoken, &st.FeedbackQueued, &last)
	if err != nil {
		return st, err
	}
	st.Accuracy, st.Accuracy7d = pct(st.MCQCorrect, st.MCQAnswered), pct(c7, n7)
	if written.Valid {
		v := float64(int(written.Float64*10+0.5)) / 10
		st.WrittenAvg = &v
	}
	if spoken.Valid {
		v := float64(int(spoken.Float64*10+0.5)) / 10
		st.SpokenAvg = &v
	}
	if last.Valid {
		t, _ := time.Parse(time.RFC3339, last.String)
		st.LastAnsweredAt = &t
	}
	if st.Days, err = s.QuizHistory(ctx, userID, 14); err != nil {
		return st, err
	}
	if st.FollowThrough7d, st.DrillsChecked7d, err = s.FollowThrough(ctx, userID, weekAgo); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drills WHERE user_id = ? AND status = 'open'`, userID).Scan(&st.DrillsOpen); err != nil {
		return st, err
	}
	// Streak: consecutive days with an answer, counting back from today (or
	// yesterday, so an unanswered morning doesn't reset it).
	answered := map[string]bool{}
	for _, d := range st.Days {
		if d.Answered > 0 {
			answered[d.Day] = true
		}
	}
	day := today
	if !answered[day.Format("2006-01-02")] {
		day = day.AddDate(0, 0, -1)
	}
	for answered[day.Format("2006-01-02")] {
		st.Streak++
		day = day.AddDate(0, 0, -1)
	}
	return st, nil
}
