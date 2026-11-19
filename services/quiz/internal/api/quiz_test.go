package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ayush3923/project-dzinga/services/quiz/internal/store"
)

func quizServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := filepath.Join(t.TempDir(), "audio")
	s := &Server{Store: st, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), InternalToken: token, UserID: "owner",
		Now: func() time.Time { return time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC) }, AudioDir: dir}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, dir
}

func quizBody() map[string]any {
	qs := []map[string]any{}
	for i := 0; i < 5; i++ {
		qs = append(qs, map[string]any{"kind": "mcq", "topic": "pm_concept", "prompt": "What does RICE stand for?",
			"options": []string{"Reach, Impact, Confidence, Effort", "Risk, Impact, Cost, Effort", "Revenue, Impact, Cost, Ease"}, "answer": 0,
			"explanation": "RICE: Reach, Impact, Confidence, Effort."})
	}
	qs = append(qs, map[string]any{"kind": "subjective", "topic": "org_news", "prompt": "What should Intuit do about agentic payroll?"},
		map[string]any{"kind": "voice", "topic": "ai_news", "prompt": "Explain MCP to an executive in one minute."})
	return map[string]any{"day": "2026-11-17", "model": "claude-opus-5-5", "questions": qs}
}

func put(t *testing.T, url, contentType string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestQuizAPI(t *testing.T) {
	ts, dir := quizServer(t)
	type quizResp struct {
		Day      string      `json:"day"`
		Quiz     *store.Quiz `json:"quiz"`
		QueuedAt *time.Time  `json:"queued_at"`
	}
	if r := decodeBody[quizResp](t, do(t, "GET", ts.URL+"/api/v1/quiz", "", nil)); r.Day != "2026-11-17" || r.Quiz != nil {
		t.Fatalf("before saving: %+v", r)
	}
	if res := do(t, "GET", ts.URL+"/api/v1/quiz?day=../../x", "", nil); res.StatusCode != 400 {
		t.Errorf("bad day: %d", res.StatusCode)
	}
	// Saving needs the internal token, and the shape is checked.
	if res := do(t, "PUT", ts.URL+"/internal/v1/quizzes", "", quizBody()); res.StatusCode != 401 {
		t.Errorf("no token: %d", res.StatusCode)
	}
	bad := quizBody()
	bad["questions"] = bad["questions"].([]map[string]any)[1:]
	if res := do(t, "PUT", ts.URL+"/internal/v1/quizzes", "Bearer "+token, bad); res.StatusCode != 400 {
		t.Errorf("4 MCQs accepted: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/quizzes", "Bearer "+token, quizBody()); res.StatusCode != 204 {
		t.Fatalf("save: %d", res.StatusCode)
	}
	r := decodeBody[quizResp](t, do(t, "GET", ts.URL+"/api/v1/quiz", "", nil))
	if r.Quiz == nil || len(r.Quiz.Questions) != 7 || r.Quiz.Questions[0].Answer != nil {
		t.Fatalf("quiz: %+v", r.Quiz)
	}
	mcq, voice := r.Quiz.Questions[0].ID, r.Quiz.Questions[6].ID
	id := func(n int64) string { return ts.URL + "/api/v1/quiz/questions/" + itoa(n) }

	x := decodeBody[store.QuizQuestion](t, do(t, "POST", id(mcq)+"/answer", "", map[string]any{"choice": 0}))
	if x.Response == nil || !*x.Response.Correct || x.Explanation == "" {
		t.Errorf("mcq: %+v", x)
	}
	if res := do(t, "POST", id(mcq)+"/answer", "", map[string]any{"choice": 1}); res.StatusCode != 409 {
		t.Errorf("second answer: %d", res.StatusCode)
	}

	// Voice: type, content and size are checked; the stored name is generated.
	webm := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, 2048)...)
	if res := put(t, id(voice)+"/voice", "audio/webm", []byte("not audio at all, just text pretending")); res.StatusCode != 415 {
		t.Errorf("fake audio: %d", res.StatusCode)
	}
	if res := put(t, id(voice)+"/voice", "text/html", webm); res.StatusCode != 415 {
		t.Errorf("html type: %d", res.StatusCode)
	}
	if res := put(t, id(voice)+"/voice", "audio/webm", append(webm, make([]byte, 9<<20)...)); res.StatusCode != 413 {
		t.Errorf("oversized: %d", res.StatusCode)
	}
	if res := put(t, id(mcq)+"/voice", "audio/webm", webm); res.StatusCode != 400 {
		t.Errorf("voice on an MCQ: %d", res.StatusCode)
	}
	if res := put(t, id(voice)+"/voice", "audio/webm;codecs=opus", webm); res.StatusCode != 200 {
		t.Fatalf("voice: %d", res.StatusCode)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatalf("files: %v", files)
	}
	if info, _ := files[0].Info(); info.Mode().Perm() != 0o600 {
		t.Errorf("audio file mode %v", info.Mode().Perm())
	}
	// Re-recording replaces the old file.
	time.Sleep(2 * time.Millisecond)
	if res := put(t, id(voice)+"/voice", "audio/webm", webm); res.StatusCode != 200 {
		t.Fatalf("re-record: %d", res.StatusCode)
	}
	if res := do(t, "GET", id(voice)+"/voice", "", nil); res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/webm" {
		t.Errorf("playback: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}

	// Claude reads the question (with the audio path) and saves feedback.
	if res := do(t, "GET", ts.URL+"/internal/v1/questions/"+itoa(voice), "", nil); res.StatusCode != 401 {
		t.Errorf("internal read without token: %d", res.StatusCode)
	}
	iq := decodeBody[struct {
		AudioPath string `json:"audio_path"`
	}](t, do(t, "GET", ts.URL+"/internal/v1/questions/"+itoa(voice), "Bearer "+token, nil))
	if filepath.Dir(iq.AudioPath) != dir {
		t.Errorf("audio path %q", iq.AudioPath)
	}
	fb := map[string]any{"transcript": "MCP lets agents call tools.", "feedback": "Clear; add a business example.", "score": 7}
	if res := do(t, "PUT", ts.URL+"/internal/v1/questions/"+itoa(voice)+"/feedback", "", fb); res.StatusCode != 401 {
		t.Errorf("feedback without token: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/questions/"+itoa(voice)+"/feedback", "Bearer "+token, fb); res.StatusCode != 204 {
		t.Errorf("feedback: %d", res.StatusCode)
	}
	if res := put(t, id(voice)+"/voice", "audio/webm", webm); res.StatusCode != 409 {
		t.Errorf("re-record after feedback: %d", res.StatusCode)
	}
	if files, _ := os.ReadDir(dir); len(files) != 1 {
		t.Errorf("rejected re-record left a file: %v", files)
	}
}

const token = "test-token-0123456789abcdef0123456789"

func do(t *testing.T, method, url, auth string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func decodeBody[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestRefreshRequestAndPending(t *testing.T) {
	ts, _ := quizServer(t)
	if res := do(t, "POST", ts.URL+"/internal/v1/requests", "", nil); res.StatusCode != 401 {
		t.Errorf("no token: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/requests", "Bearer "+token, nil); res.StatusCode != 202 {
		t.Fatalf("request: %d", res.StatusCode)
	}
	if res := do(t, "POST", ts.URL+"/internal/v1/requests", "Bearer "+token, map[string]string{"day": "tomorrow"}); res.StatusCode != 400 {
		t.Errorf("bad day: %d", res.StatusCode)
	}
	if res := do(t, "GET", ts.URL+"/internal/v1/pending", "", nil); res.StatusCode != 401 {
		t.Errorf("pending without token: %d", res.StatusCode)
	}
	p := decodeBody[store.Pending](t, do(t, "GET", ts.URL+"/internal/v1/pending", "Bearer "+token, nil))
	if len(p.Quizzes) != 1 || p.Quizzes[0] != "2026-11-17" {
		t.Fatalf("pending %+v", p)
	}
	q := decodeBody[struct {
		QueuedAt *time.Time `json:"queued_at"`
	}](t, do(t, "GET", ts.URL+"/api/v1/quiz", "", nil))
	if q.QueuedAt == nil {
		t.Error("quiz page doesn't show the request")
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/quizzes", "Bearer "+token, quizBody()); res.StatusCode != 204 {
		t.Fatalf("save: %d", res.StatusCode)
	}
	if p := decodeBody[store.Pending](t, do(t, "GET", ts.URL+"/internal/v1/pending", "Bearer "+token, nil)); len(p.Quizzes) != 0 {
		t.Errorf("still pending after saving: %+v", p)
	}
}

func TestStatsAndNewsAPI(t *testing.T) {
	ts, _ := quizServer(t)
	if res := do(t, "GET", ts.URL+"/api/v1/stats", "", nil); res.StatusCode != 200 {
		t.Errorf("stats: %d", res.StatusCode)
	}
	news := map[string]any{"day": "2026-11-17", "items": []map[string]any{
		{"topic": "ai_news", "title": "Model released", "summary": "Faster for agent work.", "source_title": "Lab", "url": "https://example.com/a"},
		{"topic": "org_news", "company": "Acme", "title": "Acme raises money", "summary": "A Series B round.", "source_title": "News", "url": "https://example.com/b"},
		{"topic": "org_news", "company": "Beta", "title": "Beta ships agents", "summary": "Agents for sales teams.", "source_title": "Beta", "url": "https://example.com/c"},
	}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/news", "", news); res.StatusCode != 401 {
		t.Errorf("news without token: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/news", "Bearer "+token, map[string]any{"day": "2026-11-17", "items": []any{}}); res.StatusCode != 400 {
		t.Errorf("empty news: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/news", "Bearer "+token, news); res.StatusCode != 204 {
		t.Fatalf("news: %d", res.StatusCode)
	}
	n := decodeBody[store.News](t, do(t, "GET", ts.URL+"/api/v1/news", "", nil))
	if n.Day != "2026-11-17" || len(n.Items) != 3 {
		t.Errorf("feed %+v", n)
	}
}

func TestPrepAPI(t *testing.T) {
	ts, _ := quizServer(t)
	item := map[string]any{"company": "Acme", "role": "Senior PM", "question": "How would you size the market for agentic payroll?",
		"why": "Round two is a product-sense case.", "approach": "Define the user, estimate bottom-up, state the assumption that matters most."}
	prep := map[string]any{"day": "2026-11-16", "items": []any{item, item, item}}
	if res := do(t, "PUT", ts.URL+"/internal/v1/prep", "", prep); res.StatusCode != 401 {
		t.Errorf("prep without token: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/prep", "Bearer "+token, map[string]any{"day": "2026-11-16", "items": []any{item}}); res.StatusCode != 400 {
		t.Errorf("one question accepted: %d", res.StatusCode)
	}
	if res := do(t, "PUT", ts.URL+"/internal/v1/prep", "Bearer "+token, prep); res.StatusCode != 204 {
		t.Fatalf("prep: %d", res.StatusCode)
	}
	// The server's today is 2026-11-17; with none for today, the newest earlier day shows.
	p := decodeBody[store.Prep](t, do(t, "GET", ts.URL+"/api/v1/prep", "", nil))
	if p.Day != "2026-11-16" || len(p.Items) != 3 || p.Items[0].Company != "Acme" {
		t.Errorf("prep %+v", p)
	}
}
