// Package api is the organizations service's HTTP surface. The contract lives in api/orgs.yaml.
package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	mrand "math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/ayush3923/project-dzinga/services/orgs/internal/store"
)

const maxBody = 1 << 20 // 1 MiB

// Apps supplies the user's applications (core-api's analysis).
type Apps interface {
	Applications(ctx context.Context) ([]json.RawMessage, error)
}

type Server struct {
	Store         *store.Store
	Core          Apps
	Log           *slog.Logger
	InternalToken string // shared secret for service-to-service calls
	UserID        string // single local user
	Now           func() time.Time
	Rand          *mrand.Rand // picks organizations to diagnose; nil = seeded per call
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	mux.HandleFunc("GET /api/v1/organizations", s.organizations)
	mux.HandleFunc("GET /api/v1/organizations/lookup", s.lookup)
	mux.HandleFunc("GET /api/v1/organizations/{id}", s.organization)
	mux.HandleFunc("PUT /api/v1/organizations/preferred", s.putPreferred)
	mux.HandleFunc("POST /api/v1/organizations/{id}/research", s.requestResearch)
	mux.HandleFunc("POST /api/v1/organizations/{id}/diagnosis-request", s.requestDiagnosis)
	mux.HandleFunc("POST /api/v1/organizations/{id}/seen", s.seen)
	mux.HandleFunc("POST /api/v1/organizations/{id}/briefing/seen", s.seen) // older name
	mux.HandleFunc("GET /api/v1/suggestions", s.suggestions)

	mux.Handle("GET /internal/v1/research-requests", s.requireInternal(http.HandlerFunc(s.pendingResearch)))
	mux.Handle("POST /internal/v1/diagnosis-requests", s.requireInternal(http.HandlerFunc(s.queueDiagnoses)))
	mux.Handle("PUT /internal/v1/organizations/{id}/profile", s.requireInternal(http.HandlerFunc(s.putCompanyProfile)))
	mux.Handle("POST /internal/v1/organizations/{id}/diagnosis", s.requireInternal(http.HandlerFunc(s.postDiagnosis)))
	mux.Handle("PUT /internal/v1/organizations/{id}/briefing", s.requireInternal(http.HandlerFunc(s.putBriefing)))
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

func (s *Server) writeResult(w http.ResponseWriter, r *http.Request, err error, okStatus int, body any) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case err != nil:
		s.internalErr(w, r, err)
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

// apps fetches applications from core-api; on failure it answers 502 and returns false.
func (s *Server) apps(w http.ResponseWriter, r *http.Request) ([]store.App, bool) {
	raw, err := s.Core.Applications(r.Context())
	if err == nil {
		var apps []store.App
		if apps, err = store.DecodeApps(raw); err == nil {
			return apps, true
		}
	}
	s.Log.Error("core_api_unavailable", "route", r.Pattern, "err", err)
	writeErr(w, http.StatusBadGateway, "core-api is not reachable")
	return nil, false
}

func (s *Server) organizations(w http.ResponseWriter, r *http.Request) {
	apps, ok := s.apps(w, r)
	if !ok {
		return
	}
	rep, err := s.Store.Organizations(r.Context(), s.UserID, apps, s.Now())
	s.writeResult(w, r, err, http.StatusOK, rep)
}

func (s *Server) organization(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	apps, ok := s.apps(w, r)
	if !ok {
		return
	}
	d, err := s.Store.Organization(r.Context(), s.UserID, id, apps, s.Now())
	s.writeResult(w, r, err, http.StatusOK, d)
}

// lookup finds an organization's id by company name (the pipeline links by company).
func (s *Server) lookup(w http.ResponseWriter, r *http.Request) {
	company := r.URL.Query().Get("company")
	if len(company) > 200 || store.OrgKey(company) == "" {
		writeErr(w, http.StatusBadRequest, "company must have letters or digits")
		return
	}
	id, err := s.Store.OrgID(r.Context(), s.UserID, company)
	s.writeResult(w, r, err, http.StatusOK, map[string]int64{"id": id})
}

func (s *Server) putPreferred(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Names []string `json:"names"`
		Auto  bool     `json:"auto"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Names) > 20 {
		writeErr(w, http.StatusBadRequest, "at most 20 preferred organizations")
		return
	}
	for _, n := range body.Names {
		if len(n) > 120 || store.OrgKey(n) == "" {
			writeErr(w, http.StatusBadRequest, "each name needs letters or digits and at most 120 characters")
			return
		}
	}
	if err := s.Store.SetPreferred(r.Context(), s.UserID, body.Names, body.Auto); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.organizations(w, r)
}

func (s *Server) requestResearch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	at, err := s.Store.RequestResearch(r.Context(), s.UserID, store.KindProfile, id, s.Now())
	s.writeResult(w, r, err, http.StatusAccepted, map[string]any{"research_requested_at": at})
}

func (s *Server) requestDiagnosis(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	at, err := s.Store.RequestResearch(r.Context(), s.UserID, store.KindDiagnosis, id, s.Now())
	s.writeResult(w, r, err, http.StatusAccepted, map[string]any{"diagnosis_requested_at": at})
}

// queueDiagnoses runs after each refresh (the sync service calls it): a few
// organizations at random get a diagnosis queued for Claude in the app.
func (s *Server) queueDiagnoses(w http.ResponseWriter, r *http.Request) {
	apps, ok := s.apps(w, r)
	if !ok {
		return
	}
	list, err := s.Store.QueueDiagnoses(r.Context(), s.UserID, apps, store.DiagnosesPerRefresh, s.Now(), s.Rand)
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"queued": list})
}

func (s *Server) pendingResearch(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.PendingResearch(r.Context(), s.UserID)
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"requests": list})
}

func (s *Server) suggestions(w http.ResponseWriter, r *http.Request) {
	apps, ok := s.apps(w, r)
	if !ok {
		return
	}
	list, err := s.Store.Suggestions(r.Context(), s.UserID, apps, s.Now())
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"suggestions": list})
}

func (s *Server) putCompanyProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var p store.CompanyProfile
	if !decode(w, r, &p) {
		return
	}
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SaveCompanyProfile(r.Context(), s.UserID, id, p, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) postDiagnosis(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var d store.Diagnosis
	if !decode(w, r, &d) {
		return
	}
	if err := d.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SaveDiagnosis(r.Context(), s.UserID, id, d, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) putBriefing(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var b store.Briefing
	if !decode(w, r, &b) {
		return
	}
	if err := b.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SaveBriefing(r.Context(), s.UserID, id, b, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) seen(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	s.writeResult(w, r, s.Store.MarkSeen(r.Context(), s.UserID, id, s.Now()), http.StatusNoContent, nil)
}
