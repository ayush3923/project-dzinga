// Package api is the startoff service's HTTP surface. The contract lives in api/startoff.yaml.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ayush3923/project-dzinga/services/startoff/internal/jobs"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/peers"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/store"
)

const maxBody = 1 << 20 // 1 MiB

// Orgs resolves an organization id to its name (the organizations service).
type Orgs interface {
	Org(ctx context.Context, id int64) (store.Org, error)
}

type Server struct {
	Store         *store.Store
	Orgs          Orgs
	Runner        *jobs.Runner
	Log           *slog.Logger
	InternalToken string // shared secret for service-to-service calls
	UserID        string // single local user
	Now           func() time.Time
	// Background runs the daily run's work; tests run it inline.
	Background func(func())
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	// Read by the web app.
	mux.HandleFunc("GET /api/v1/organizations/flags", s.flags)
	mux.HandleFunc("GET /api/v1/organizations/{id}", s.research)
	mux.HandleFunc("GET /api/v1/organizations/{id}/history", s.history)
	mux.HandleFunc("POST /api/v1/organizations/{id}/research", s.request(store.KindProfile, "research_requested_at"))
	mux.HandleFunc("POST /api/v1/organizations/{id}/diagnosis-request", s.request(store.KindDiagnosis, "diagnosis_requested_at"))
	mux.HandleFunc("POST /api/v1/organizations/{id}/seen", s.seen)
	mux.HandleFunc("GET /api/v1/runs", s.runs)
	mux.HandleFunc("POST /api/v1/runs", s.startRun(store.TriggerManual))

	// Claude in the app (docs/runbooks/claude-queue.md).
	mux.Handle("POST /internal/v1/runs", s.requireInternal(s.startRun(store.TriggerClaude)))
	mux.Handle("GET /internal/v1/research-requests", s.requireInternal(http.HandlerFunc(s.pending)))
	mux.Handle("PUT /internal/v1/organizations/{id}/profile", s.requireInternal(http.HandlerFunc(s.putProfile)))
	mux.Handle("PUT /internal/v1/organizations/{id}/briefing", s.requireInternal(http.HandlerFunc(s.putBriefing)))
	mux.Handle("POST /internal/v1/organizations/{id}/diagnosis", s.requireInternal(http.HandlerFunc(s.postDiagnosis)))
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

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, err error, okStatus int, body any) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, peers.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case err != nil:
		s.Log.Error("handler_error", "route", r.Pattern, "err", err)
		writeErr(w, http.StatusInternalServerError, "internal error")
	case body == nil:
		w.WriteHeader(okStatus)
	default:
		writeJSON(w, okStatus, body)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeErr(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	return true
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// org resolves the path's organization with the organizations service; on failure
// it answers (400, 404 or 502) and returns false.
func (s *Server) org(w http.ResponseWriter, r *http.Request) (store.Org, bool) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return store.Org{}, false
	}
	o, err := s.Orgs.Org(r.Context(), id)
	switch {
	case errors.Is(err, peers.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
		return o, false
	case err != nil:
		s.Log.Error("orgs_unavailable", "route", r.Pattern, "err", err)
		writeErr(w, http.StatusBadGateway, "the organizations service is not reachable")
		return o, false
	}
	return o, true
}

// ---------- Research ----------

func (s *Server) flags(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.AllFlags(r.Context(), s.UserID)
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"flags": list})
}

func (s *Server) research(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	res, err := s.Store.OrgResearch(r.Context(), s.UserID, id)
	s.writeResult(w, r, err, http.StatusOK, res)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	h, err := s.Store.OrgHistory(r.Context(), s.UserID, id)
	s.writeResult(w, r, err, http.StatusOK, h)
}

func (s *Server) request(kind, field string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		o, ok := s.org(w, r)
		if !ok {
			return
		}
		at, err := s.Store.RequestResearch(r.Context(), s.UserID, kind, o, 0, s.Now())
		s.writeResult(w, r, err, http.StatusAccepted, map[string]any{field: at})
	}
}

func (s *Server) seen(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	s.writeResult(w, r, s.Store.MarkSeen(r.Context(), s.UserID, id, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) pending(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.PendingResearch(r.Context(), s.UserID)
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"requests": list})
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	var p store.CompanyProfile
	if !decode(w, r, &p) {
		return
	}
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if o, ok := s.org(w, r); ok {
		s.writeResult(w, r, s.Store.SaveProfile(r.Context(), s.UserID, o, p, s.Now()), http.StatusNoContent, nil)
	}
}

func (s *Server) putBriefing(w http.ResponseWriter, r *http.Request) {
	var b store.Briefing
	if !decode(w, r, &b) {
		return
	}
	if err := b.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if o, ok := s.org(w, r); ok {
		s.writeResult(w, r, s.Store.SaveBriefing(r.Context(), s.UserID, o, b, s.Now()), http.StatusNoContent, nil)
	}
}

func (s *Server) postDiagnosis(w http.ResponseWriter, r *http.Request) {
	var d store.Diagnosis
	if !decode(w, r, &d) {
		return
	}
	if err := d.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if o, ok := s.org(w, r); ok {
		s.writeResult(w, r, s.Store.SaveDiagnosis(r.Context(), s.UserID, o, d, s.Now()), http.StatusNoContent, nil)
	}
}

// ---------- Daily runs ----------

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeErr(w, http.StatusBadRequest, "limit must be 1-100")
			return
		}
		limit = n
	}
	list, err := s.Store.Runs(r.Context(), s.UserID, limit)
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"runs": list})
}

// startRun begins a daily run in the background. A run already in progress is
// returned as is (200), a new one with 202.
func (s *Server) startRun(trigger string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		run, err := s.Runner.Begin(r.Context(), trigger)
		if errors.Is(err, store.ErrRunning) {
			writeJSON(w, http.StatusOK, run)
			return
		}
		if err != nil {
			s.writeResult(w, r, err, 0, nil)
			return
		}
		// Start the mail refresh before answering, so the web app's progress poll sees
		// it running; the run's refresh step joins it.
		if err := s.Runner.Ingest.Start(r.Context()); err != nil {
			s.Log.Warn("refresh_not_started", "run_id", run.ID, "err", err)
		}
		s.Background(func() { s.Runner.Execute(context.Background(), run) })
		writeJSON(w, http.StatusAccepted, run)
	}
}
