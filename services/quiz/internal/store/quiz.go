package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The daily quiz: five multiple-choice questions, one written and one spoken, built
// by Claude from AI news, product-management concepts and news about companies the
// user applied to. A refresh requests it (RequestQuiz); Claude saves it with SaveQuiz.
// Written and spoken answers without feedback are pending until SaveFeedback.

var (
	ErrQuizExists = errors.New("a quiz for this day already exists")
	ErrAnswered   = errors.New("this question is already answered")
	ErrBadAnswer  = errors.New("invalid answer for this question")
	reDay         = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reAudioFile   = regexp.MustCompile(`^q\d+-\d+\.(webm|ogg|m4a)$`)
	quizShape     = map[string]int{"mcq": 5, "subjective": 1, "voice": 1}
	quizTopics    = map[string]bool{"ai_news": true, "pm_concept": true, "org_news": true}
)

type QuizQuestion struct {
	ID          int64       `json:"id"`
	Position    int         `json:"position"`
	Kind        string      `json:"kind"`  // mcq | subjective | voice
	Topic       string      `json:"topic"` // ai_news | pm_concept | org_news
	Prompt      string      `json:"prompt"`
	Options     []string    `json:"options"`
	Answer      *int        `json:"answer"`      // MCQ: hidden until answered
	Explanation string      `json:"explanation"` // MCQ: hidden until answered
	SourceTitle string      `json:"source_title"`
	SourceURL   string      `json:"source_url"`
	Response    *QuizAnswer `json:"response"`
}

type QuizAnswer struct {
	Choice     *int       `json:"choice"`
	Text       string     `json:"text"`
	AudioFile  string     `json:"audio_file"`
	Transcript string     `json:"transcript"`
	Feedback   string     `json:"feedback"`
	Score      *int       `json:"score"`
	Correct    *bool      `json:"correct"` // MCQ only
	AnsweredAt time.Time  `json:"answered_at"`
	FeedbackAt *time.Time `json:"feedback_at"`
}

type Quiz struct {
	Day       string         `json:"day"`
	Model     string         `json:"model"`
	CreatedAt time.Time      `json:"created_at"`
	Questions []QuizQuestion `json:"questions"`
}

// QuizDay is one row of the quiz history.
type QuizDay struct {
	Day      string `json:"day"`
	Answered int    `json:"answered"`
	Correct  int    `json:"correct"` // MCQ answered correctly
	Total    int    `json:"total"`
}

// Validate checks the quiz has the agreed shape before it is saved.
func (q Quiz) Validate() error {
	if !reDay.MatchString(q.Day) {
		return errors.New("day must be YYYY-MM-DD")
	}
	if strings.TrimSpace(q.Model) == "" {
		return errors.New("model is required")
	}
	count := map[string]int{}
	for i, x := range q.Questions {
		if _, ok := quizShape[x.Kind]; !ok {
			return fmt.Errorf("question %d: kind must be mcq, subjective or voice", i+1)
		}
		count[x.Kind]++
		if !quizTopics[x.Topic] {
			return fmt.Errorf("question %d: topic must be ai_news, pm_concept or org_news", i+1)
		}
		if n := len([]rune(strings.TrimSpace(x.Prompt))); n < 10 || n > 1000 {
			return fmt.Errorf("question %d: prompt must be 10-1000 characters", i+1)
		}
		if x.SourceURL != "" && !strings.HasPrefix(x.SourceURL, "https://") {
			return fmt.Errorf("question %d: source_url must be https", i+1)
		}
		if x.Kind == "mcq" {
			if len(x.Options) < 3 || len(x.Options) > 5 {
				return fmt.Errorf("question %d: an MCQ needs 3-5 options", i+1)
			}
			for _, o := range x.Options {
				if strings.TrimSpace(o) == "" || len([]rune(o)) > 300 {
					return fmt.Errorf("question %d: options must be 1-300 characters", i+1)
				}
			}
			if x.Answer == nil || *x.Answer < 0 || *x.Answer >= len(x.Options) {
				return fmt.Errorf("question %d: answer must index an option", i+1)
			}
		} else if len(x.Options) > 0 || x.Answer != nil {
			return fmt.Errorf("question %d: only MCQs have options and an answer", i+1)
		}
	}
	for kind, want := range quizShape {
		if count[kind] != want {
			return fmt.Errorf("need %d %s question(s), got %d", want, kind, count[kind])
		}
	}
	return nil
}

// SaveQuiz stores a day's quiz and marks its request done. A day's quiz can be
// replaced only while none of it has been answered.
func (s *Store) SaveQuiz(ctx context.Context, userID string, q Quiz, now time.Time) error {
	if err := q.Validate(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var old int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM quizzes WHERE user_id = ? AND day = ?`, userID, q.Day).Scan(&old)
	switch {
	case err == nil:
		var answered int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM quiz_answers qa JOIN quiz_questions qq ON qq.id = qa.question_id
			WHERE qq.quiz_id = ?`, old).Scan(&answered); err != nil {
			return err
		}
		if answered > 0 {
			return ErrQuizExists
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM quizzes WHERE id = ?`, old); err != nil {
			return err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO quizzes (user_id, day, model, created_at) VALUES (?, ?, ?, ?)`,
		userID, q.Day, truncate(q.Model, 100), now.UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	quizID, _ := res.LastInsertId()
	for i, x := range q.Questions {
		opts, _ := json.Marshal(x.Options)
		if x.Options == nil {
			opts = []byte("[]")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO quiz_questions (quiz_id, position, kind, topic, prompt, options, answer, explanation, source_title, source_url)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, quizID, i, x.Kind, x.Topic, strings.TrimSpace(x.Prompt), string(opts), x.Answer,
			truncate(x.Explanation, 1000), truncate(x.SourceTitle, 200), truncate(x.SourceURL, 2000)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE quiz_requests SET done_at = ? WHERE user_id = ? AND day = ? AND done_at IS NULL`,
		now.UTC().Format(time.RFC3339), userID, q.Day); err != nil {
		return err
	}
	return tx.Commit()
}

// QuizForDay returns a day's quiz. reveal=false hides MCQ answers until answered
// (the web app); reveal=true shows everything (Claude, writing feedback).
func (s *Store) QuizForDay(ctx context.Context, userID, day string, reveal bool) (Quiz, error) {
	q := Quiz{Day: day, Questions: []QuizQuestion{}}
	var id int64
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT id, model, created_at FROM quizzes WHERE user_id = ? AND day = ?`, userID, day).Scan(&id, &q.Model, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return q, ErrNotFound
	}
	if err != nil {
		return q, err
	}
	q.CreatedAt, _ = time.Parse(time.RFC3339, created)
	err = s.queryRows(ctx, `SELECT `+questionColumns+` FROM quiz_questions qq LEFT JOIN quiz_answers qa ON qa.question_id = qq.id
		WHERE qq.quiz_id = ? ORDER BY qq.position`, []any{id}, func(rows *sql.Rows) error {
		x, err := scanQuestion(rows, reveal)
		q.Questions = append(q.Questions, x)
		return err
	})
	return q, err
}

const questionColumns = `qq.id, qq.position, qq.kind, qq.topic, qq.prompt, qq.options, qq.answer, qq.explanation, qq.source_title, qq.source_url,
	qa.question_id IS NOT NULL, qa.choice, COALESCE(qa.text, ''), COALESCE(qa.audio_file, ''), COALESCE(qa.transcript, ''),
	COALESCE(qa.feedback, ''), qa.score, COALESCE(qa.answered_at, ''), qa.feedback_at`

func scanQuestion(rows interface{ Scan(...any) error }, reveal bool) (QuizQuestion, error) {
	var x QuizQuestion
	var opts, answeredAt string
	var answer, choice, score sql.NullInt64
	var hasAnswer bool
	var feedbackAt sql.NullString
	var a QuizAnswer
	if err := rows.Scan(&x.ID, &x.Position, &x.Kind, &x.Topic, &x.Prompt, &opts, &answer, &x.Explanation, &x.SourceTitle, &x.SourceURL,
		&hasAnswer, &choice, &a.Text, &a.AudioFile, &a.Transcript, &a.Feedback, &score, &answeredAt, &feedbackAt); err != nil {
		return x, err
	}
	x.Options = []string{}
	_ = json.Unmarshal([]byte(opts), &x.Options)
	if hasAnswer {
		a.AnsweredAt, _ = time.Parse(time.RFC3339, answeredAt)
		if choice.Valid {
			c := int(choice.Int64)
			a.Choice = &c
			ok := answer.Valid && int64(c) == answer.Int64
			a.Correct = &ok
		}
		if score.Valid {
			v := int(score.Int64)
			a.Score = &v
		}
		if feedbackAt.Valid {
			t, _ := time.Parse(time.RFC3339, feedbackAt.String)
			a.FeedbackAt = &t
		}
		x.Response = &a
	}
	if answer.Valid && (reveal || hasAnswer) {
		v := int(answer.Int64)
		x.Answer = &v
	}
	if !reveal && !hasAnswer && x.Kind == "mcq" {
		x.Explanation = ""
	}
	return x, nil
}

// Question returns one question with its answer, checking it belongs to the user.
func (s *Store) Question(ctx context.Context, userID string, questionID int64, reveal bool) (QuizQuestion, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+questionColumns+` FROM quiz_questions qq
		JOIN quizzes z ON z.id = qq.quiz_id LEFT JOIN quiz_answers qa ON qa.question_id = qq.id
		WHERE qq.id = ? AND z.user_id = ?`, questionID, userID)
	x, err := scanQuestion(row, reveal)
	if errors.Is(err, sql.ErrNoRows) {
		return x, ErrNotFound
	}
	return x, err
}

// AnswerQuestion records an answer. An MCQ is answered once, and the result is
// shown at once. A written or spoken answer can be replaced until Claude has given
// feedback on it; until then it is pending (see Pending).
func (s *Store) AnswerQuestion(ctx context.Context, userID string, questionID int64, choice *int, text, audioFile string, now time.Time) (QuizQuestion, error) {
	x, err := s.Question(ctx, userID, questionID, false)
	if err != nil {
		return x, err
	}
	text = strings.TrimSpace(text)
	switch x.Kind {
	case "mcq":
		if x.Response != nil {
			return x, ErrAnswered
		}
		if choice == nil || *choice < 0 || *choice >= len(x.Options) || text != "" || audioFile != "" {
			return x, ErrBadAnswer
		}
	case "subjective":
		if choice != nil || audioFile != "" || len([]rune(text)) < 1 || len([]rune(text)) > 4000 {
			return x, ErrBadAnswer
		}
	case "voice":
		if choice != nil || text != "" || !reAudioFile.MatchString(audioFile) {
			return x, ErrBadAnswer
		}
	}
	if x.Kind != "mcq" && x.Response != nil && x.Response.FeedbackAt != nil {
		return x, ErrAnswered
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO quiz_answers (question_id, choice, text, audio_file, answered_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (question_id) DO UPDATE SET choice = excluded.choice, text = excluded.text, audio_file = excluded.audio_file,
			transcript = '', answered_at = excluded.answered_at`,
		questionID, choice, text, audioFile, now.UTC().Format(time.RFC3339)); err != nil {
		return x, err
	}
	return s.Question(ctx, userID, questionID, false)
}

// SaveFeedback stores Claude's feedback on a written or spoken answer.
func (s *Store) SaveFeedback(ctx context.Context, userID string, questionID int64, transcript, feedback string, score int, now time.Time) error {
	if strings.TrimSpace(feedback) == "" || score < 0 || score > 10 {
		return errors.New("feedback is required and score must be 0-10")
	}
	x, err := s.Question(ctx, userID, questionID, true)
	if err != nil {
		return err
	}
	if x.Kind == "mcq" || x.Response == nil {
		return ErrBadAnswer
	}
	_, err = s.db.ExecContext(ctx, `UPDATE quiz_answers SET transcript = ?, feedback = ?, score = ?, feedback_at = ? WHERE question_id = ?`,
		truncate(strings.TrimSpace(transcript), 8000), truncate(strings.TrimSpace(feedback), 2000), score, now.UTC().Format(time.RFC3339), questionID)
	return err
}

// QuizHistory lists recent quizzes, newest first.
func (s *Store) QuizHistory(ctx context.Context, userID string, limit int) ([]QuizDay, error) {
	out := []QuizDay{}
	err := s.queryRows(ctx, `SELECT z.day, COUNT(qq.id), COUNT(qa.question_id),
			COALESCE(SUM(CASE WHEN qq.kind = 'mcq' AND qa.choice = qq.answer THEN 1 ELSE 0 END), 0)
		FROM quizzes z JOIN quiz_questions qq ON qq.quiz_id = z.id LEFT JOIN quiz_answers qa ON qa.question_id = qq.id
		WHERE z.user_id = ? GROUP BY z.id ORDER BY z.day DESC LIMIT ?`, []any{userID, limit}, func(rows *sql.Rows) error {
		var d QuizDay
		err := rows.Scan(&d.Day, &d.Total, &d.Answered, &d.Correct)
		out = append(out, d)
		return err
	})
	return out, err
}

// RequestQuiz asks for a day's quiz (a refresh's last step). It does nothing when
// the quiz exists or is already requested.
func (s *Store) RequestQuiz(ctx context.Context, userID, day string, now time.Time) error {
	if !reDay.MatchString(day) {
		return errors.New("day must be YYYY-MM-DD")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO quiz_requests (user_id, day, requested_at)
		SELECT ?, ?, ? WHERE NOT EXISTS (SELECT 1 FROM quizzes WHERE user_id = ? AND day = ?)
		ON CONFLICT DO NOTHING`, userID, day, now.UTC().Format(time.RFC3339), userID, day)
	return err
}

// QuizQueuedAt reports when a day's quiz was requested (nil when it isn't pending).
func (s *Store) QuizQueuedAt(ctx context.Context, userID, day string) (*time.Time, error) {
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT requested_at FROM quiz_requests WHERE user_id = ? AND day = ? AND done_at IS NULL`,
		userID, day).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, _ := time.Parse(time.RFC3339, at)
	return &t, nil
}

// PendingFeedback is a written or spoken answer waiting for Claude.
type PendingFeedback struct {
	QuestionID int64  `json:"question_id"`
	Day        string `json:"day"`
	Kind       string `json:"kind"`
	Prompt     string `json:"prompt"`
}

// Pending is Claude's quiz work: days whose quiz, key news or prep questions are missing, and answers awaiting feedback.
type Pending struct {
	Quizzes  []string          `json:"quizzes"`
	News     []string          `json:"news"` // requested days (last 3) with no key news yet
	Feedback []PendingFeedback `json:"feedback"`
	Prep     []string          `json:"prep"` // today, when its bonus prep questions are missing (a past day's are no use)
}

func (s *Store) Pending(ctx context.Context, userID string, now time.Time) (Pending, error) {
	p := Pending{Quizzes: []string{}, News: []string{}, Feedback: []PendingFeedback{}, Prep: []string{}}
	if err := s.queryRows(ctx, `SELECT day FROM quiz_requests WHERE user_id = ? AND done_at IS NULL ORDER BY day`,
		[]any{userID}, func(r *sql.Rows) error {
			var d string
			err := r.Scan(&d)
			p.Quizzes = append(p.Quizzes, d)
			return err
		}); err != nil {
		return p, err
	}
	if err := s.queryRows(ctx, `SELECT r.day FROM quiz_requests r WHERE r.user_id = ?
		AND r.day >= date(?, '-2 days') AND NOT EXISTS (SELECT 1 FROM news_items n WHERE n.user_id = r.user_id AND n.day = r.day)
		ORDER BY r.day`, []any{userID, now.In(IST).Format("2006-01-02")}, func(r *sql.Rows) error {
		var d string
		err := r.Scan(&d)
		p.News = append(p.News, d)
		return err
	}); err != nil {
		return p, err
	}
	if err := s.queryRows(ctx, `SELECT r.day FROM quiz_requests r WHERE r.user_id = ?
		AND r.day = ? AND NOT EXISTS (SELECT 1 FROM prep_questions p WHERE p.user_id = r.user_id AND p.day = r.day)
		ORDER BY r.day`, []any{userID, now.In(IST).Format("2006-01-02")}, func(r *sql.Rows) error {
		var d string
		err := r.Scan(&d)
		p.Prep = append(p.Prep, d)
		return err
	}); err != nil {
		return p, err
	}
	err := s.queryRows(ctx, `SELECT qq.id, z.day, qq.kind, qq.prompt FROM quiz_answers qa
		JOIN quiz_questions qq ON qq.id = qa.question_id JOIN quizzes z ON z.id = qq.quiz_id
		WHERE z.user_id = ? AND qq.kind <> 'mcq' AND qa.feedback_at IS NULL ORDER BY qa.answered_at`,
		[]any{userID}, func(r *sql.Rows) error {
			var f PendingFeedback
			err := r.Scan(&f.QuestionID, &f.Day, &f.Kind, &f.Prompt)
			p.Feedback = append(p.Feedback, f)
			return err
		})
	return p, err
}
