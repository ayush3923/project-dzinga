// Package api is the quiz service's HTTP surface. The contract lives in api/quiz.yaml.
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ayush3923/project-dzinga/services/quiz/internal/store"
)

const maxBody = 1 << 20 // 1 MiB

type Server struct {
	Store         *store.Store
	Log           *slog.Logger
	InternalToken string // shared secret for service-to-service calls
	UserID        string // prototype: single local user
	Now           func() time.Time
	AudioDir      string // spoken answers; this service's disk only
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	// Called by the web app's server.
	mux.HandleFunc("GET /api/v1/quiz", s.getQuiz)
	mux.HandleFunc("POST /api/v1/quiz/questions/{id}/answer", s.answerQuestion)
	mux.HandleFunc("PUT /api/v1/quiz/questions/{id}/voice", s.putVoice)
	mux.HandleFunc("GET /api/v1/quiz/questions/{id}/voice", s.getVoice)
	mux.HandleFunc("GET /api/v1/stats", s.stats)
	mux.HandleFunc("GET /api/v1/news", s.news)
	// Called by the sync service (after a refresh) and by Claude's scheduled run.
	mux.Handle("POST /internal/v1/requests", s.requireInternal(http.HandlerFunc(s.requestQuiz)))
	mux.Handle("GET /internal/v1/pending", s.requireInternal(http.HandlerFunc(s.pending)))
	mux.Handle("PUT /internal/v1/quizzes", s.requireInternal(http.HandlerFunc(s.putQuiz)))
	mux.Handle("PUT /internal/v1/news", s.requireInternal(http.HandlerFunc(s.putNews)))
	mux.Handle("GET /internal/v1/questions/{id}", s.requireInternal(http.HandlerFunc(s.internalQuestion)))
	mux.Handle("PUT /internal/v1/questions/{id}/feedback", s.requireInternal(http.HandlerFunc(s.putFeedback)))
	return s.observe(securityHeaders(mux))
}

// observe logs one structured line per request. Query strings are not logged.
func (s *Server) observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.Log.Info("http_request", "request_id", id, "method", r.Method, "route", r.Pattern,
			"status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) { r.status = code; r.ResponseWriter.WriteHeader(code) }

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireInternal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		want := "Bearer " + s.InternalToken
		if s.InternalToken == "" || subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// internalErr hides details from the client but keeps them in the logs.
func (s *Server) internalErr(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("handler_error", "route", r.Pattern, "err", err)
	writeErr(w, http.StatusInternalServerError, "internal error")
}

// writeResult maps store errors to HTTP statuses.
func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, err error, okStatus int, body any) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrQuizExists), errors.Is(err, store.ErrAnswered):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, store.ErrBadAnswer):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.internalErr(w, r, err)
	case body == nil:
		w.WriteHeader(okStatus)
	default:
		writeJSON(w, okStatus, body)
	}
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
