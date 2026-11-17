package api

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ayush3923/project-dzinga/services/quiz/internal/store"
)

// The daily quiz (see store/quiz.go). Spoken answers are stored as files in
// AudioDir on this service's disk and are never sent anywhere by this service.

const maxAudio = 8 << 20 // 8 MiB: about four minutes of Opus audio

var reDay = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// audioExt maps the browser's recording type to a file extension, and checks the
// file's first bytes match it.
func audioExt(contentType string, head []byte) (string, bool) {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch ct {
	case "audio/webm":
		return "webm", bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3})
	case "audio/ogg":
		return "ogg", bytes.HasPrefix(head, []byte("OggS"))
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return "m4a", len(head) >= 8 && string(head[4:8]) == "ftyp"
	}
	return "", false
}

func (s *Server) getQuiz(w http.ResponseWriter, r *http.Request) {
	day := r.URL.Query().Get("day")
	if day == "" {
		day = s.Now().In(store.IST).Format("2006-01-02")
	}
	if !reDay.MatchString(day) {
		writeErr(w, http.StatusBadRequest, "day must be YYYY-MM-DD")
		return
	}
	var quiz *store.Quiz
	q, err := s.Store.QuizForDay(r.Context(), s.UserID, day, false)
	switch {
	case err == nil:
		quiz = &q
	case !errors.Is(err, store.ErrNotFound):
		s.internalErr(w, r, err)
		return
	}
	queued, err := s.Store.QuizQueuedAt(r.Context(), s.UserID, day)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	history, err := s.Store.QuizHistory(r.Context(), s.UserID, 14)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"day": day, "quiz": quiz, "queued_at": queued, "history": history})
}

func (s *Server) answerQuestion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Choice *int   `json:"choice"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	x, err := s.Store.AnswerQuestion(r.Context(), s.UserID, id, body.Choice, body.Text, "", s.Now())
	s.writeResult(w, r, err, http.StatusOK, x)
}

// putVoice saves a recorded answer. The file name is generated here, never taken
// from the request.
func (s *Server) putVoice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if s.AudioDir == "" {
		writeErr(w, http.StatusServiceUnavailable, "voice answers are not configured")
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxAudio))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "the recording is too long (8 MB at most)")
		return
	}
	ext, ok := audioExt(r.Header.Get("Content-Type"), data)
	if !ok || len(data) < 1024 {
		writeErr(w, http.StatusUnsupportedMediaType, "send a WebM, Ogg or MP4 audio recording")
		return
	}
	// Check the question before writing anything to disk.
	prev, err := s.Store.Question(r.Context(), s.UserID, id, false)
	if err != nil {
		s.writeResult(w, r, err, 0, nil)
		return
	}
	if prev.Kind != "voice" {
		writeErr(w, http.StatusBadRequest, store.ErrBadAnswer.Error())
		return
	}
	if prev.Response != nil && prev.Response.FeedbackAt != nil {
		writeErr(w, http.StatusConflict, store.ErrAnswered.Error())
		return
	}
	if err := os.MkdirAll(s.AudioDir, 0o700); err != nil {
		s.internalErr(w, r, err)
		return
	}
	var rnd [4]byte
	rand.Read(rnd[:])
	name := fmt.Sprintf("q%d-%d%010d.%s", id, s.Now().UnixMilli(), binary.BigEndian.Uint32(rnd[:]), ext)
	path := filepath.Join(s.AudioDir, name)
	// O_EXCL: never overwrite an existing recording.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		s.internalErr(w, r, werr)
		return
	}
	x, err := s.Store.AnswerQuestion(r.Context(), s.UserID, id, nil, "", name, s.Now())
	if err != nil {
		os.Remove(path)
		s.writeResult(w, r, err, 0, nil)
		return
	}
	if prev.Response != nil && prev.Response.AudioFile != "" && prev.Response.AudioFile != name {
		os.Remove(filepath.Join(s.AudioDir, filepath.Base(prev.Response.AudioFile)))
	}
	writeJSON(w, http.StatusOK, x)
}

func (s *Server) getVoice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	x, err := s.Store.Question(r.Context(), s.UserID, id, false)
	if err == nil && (x.Response == nil || x.Response.AudioFile == "" || s.AudioDir == "") {
		err = store.ErrNotFound
	}
	if err != nil {
		s.writeResult(w, r, err, 0, nil)
		return
	}
	types := map[string]string{".webm": "audio/webm", ".ogg": "audio/ogg", ".m4a": "audio/mp4"}
	name := filepath.Base(x.Response.AudioFile)
	w.Header().Set("Content-Type", types[filepath.Ext(name)])
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeFile(w, r, filepath.Join(s.AudioDir, name))
}

func (s *Server) putQuiz(w http.ResponseWriter, r *http.Request) {
	var q store.Quiz
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&q); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := q.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SaveQuiz(r.Context(), s.UserID, q, s.Now()), http.StatusNoContent, nil)
}

// requestQuiz is a refresh's last step: it asks for today's quiz (IST), or for
// the day in the body.
func (s *Server) requestQuiz(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Day string `json:"day"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	if body.Day == "" {
		body.Day = s.Now().In(store.IST).Format("2006-01-02")
	}
	if !reDay.MatchString(body.Day) {
		writeErr(w, http.StatusBadRequest, "day must be YYYY-MM-DD")
		return
	}
	s.writeResult(w, r, s.Store.RequestQuiz(r.Context(), s.UserID, body.Day, s.Now()), http.StatusAccepted, map[string]string{"day": body.Day})
}

// pending is Claude's quiz work: quizzes to build and answers awaiting feedback.
func (s *Server) pending(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.Pending(r.Context(), s.UserID, s.Now())
	s.writeResult(w, r, err, http.StatusOK, p)
}

// stats is the quiz performance summary for the profile page.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Store.Stats(r.Context(), s.UserID, s.Now())
	s.writeResult(w, r, err, http.StatusOK, st)
}

// news is the latest day's key news for the profile page.
func (s *Server) news(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.LatestNews(r.Context(), s.UserID)
	s.writeResult(w, r, err, http.StatusOK, n)
}

func (s *Server) putNews(w http.ResponseWriter, r *http.Request) {
	var n store.News
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&n); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := n.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SaveNews(r.Context(), s.UserID, n), http.StatusNoContent, nil)
}

// internalQuestion gives Claude a question with its answer, and the recording's
// full path so it can be transcribed on this machine.
func (s *Server) internalQuestion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	x, err := s.Store.Question(r.Context(), s.UserID, id, true)
	audio := ""
	if err == nil && x.Response != nil && x.Response.AudioFile != "" && s.AudioDir != "" {
		audio = filepath.Join(s.AudioDir, filepath.Base(x.Response.AudioFile))
	}
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"question": x, "audio_path": audio})
}

func (s *Server) putFeedback(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Transcript string `json:"transcript"`
		Feedback   string `json:"feedback"`
		Score      int    `json:"score"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(body.Feedback) == "" || body.Score < 0 || body.Score > 10 {
		writeErr(w, http.StatusBadRequest, "feedback is required and score must be 0-10")
		return
	}
	s.writeResult(w, r, s.Store.SaveFeedback(r.Context(), s.UserID, id, body.Transcript, body.Feedback, body.Score, s.Now()), http.StatusNoContent, nil)
}
