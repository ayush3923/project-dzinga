// Package api is core-api's HTTP surface. The contract lives in api/core-api.yaml.
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
	"strings"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

const maxBody = 1 << 20 // 1 MiB

type Server struct {
	Store         *store.Store
	Log           *slog.Logger
	InternalToken string // shared secret for service-to-service calls (ingest → core-api)
	UserID        string // prototype: single local user; replaced by Cognito identity later
	Now           func() time.Time
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/v1/summary", s.summary)
	mux.HandleFunc("GET /api/v1/digest", s.digest)
	mux.Handle("PUT /internal/v1/digest", s.requireInternal(http.HandlerFunc(s.putDigestText)))
	mux.HandleFunc("GET /api/v1/applications", s.listApplications)
	mux.HandleFunc("GET /api/v1/applications/{id}", s.getApplication)
	mux.HandleFunc("PATCH /api/v1/applications/{id}", s.patchApplication)
	mux.HandleFunc("GET /api/v1/intent", s.getIntent)
	mux.HandleFunc("PUT /api/v1/intent", s.putIntent)
	mux.HandleFunc("GET /api/v1/analysis", s.analysis)
	mux.HandleFunc("GET /api/v1/applications/{id}/detail", s.detail)
	mux.HandleFunc("GET /api/v1/organizations", s.organizations)
	mux.HandleFunc("GET /api/v1/organizations/{id}", s.organization)
	mux.HandleFunc("PUT /api/v1/organizations/preferred", s.putPreferred)
	mux.HandleFunc("POST /api/v1/organizations/{id}/research", s.requestResearch("company_profile"))
	mux.HandleFunc("POST /api/v1/applications/{id}/research", s.requestResearch("job_posting"))
	mux.Handle("GET /internal/v1/research-requests", s.requireInternal(http.HandlerFunc(s.pendingResearch)))
	mux.Handle("PUT /internal/v1/organizations/{id}/profile", s.requireInternal(http.HandlerFunc(s.putCompanyProfile)))
	mux.Handle("POST /internal/v1/organizations/{id}/diagnosis", s.requireInternal(http.HandlerFunc(s.postDiagnosis)))
	mux.HandleFunc("GET /api/v1/mailboxes", s.mailboxes)
	mux.HandleFunc("POST /api/v1/mailboxes", s.addMailbox)
	mux.HandleFunc("PATCH /api/v1/mailboxes/{id}", s.patchMailbox)
	mux.HandleFunc("PUT /api/v1/sync-settings", s.putSyncSettings)
	mux.Handle("DELETE /internal/v1/mailboxes/{id}", s.requireInternal(http.HandlerFunc(s.removeMailbox)))
	mux.Handle("PUT /internal/v1/mailboxes/{id}/state", s.requireInternal(http.HandlerFunc(s.mailboxState)))
	mux.Handle("POST /internal/v1/maintenance", s.requireInternal(http.HandlerFunc(s.maintenance)))
	mux.HandleFunc("GET /api/v1/profile", s.getProfile)
	mux.HandleFunc("PUT /api/v1/profile", s.putProfile)
	mux.Handle("POST /internal/v1/events", s.requireInternal(http.HandlerFunc(s.ingest)))
	mux.Handle("PUT /internal/v1/applications/{id}/posting", s.requireInternal(http.HandlerFunc(s.putPosting)))
	mux.Handle("POST /internal/v1/applications/{id}/fit", s.requireInternal(http.HandlerFunc(s.postFit)))
	mux.Handle("POST /internal/v1/applications/{id}/outreach", s.requireInternal(http.HandlerFunc(s.postOutreach)))
	mux.Handle("POST /internal/v1/llm-usage", s.requireInternal(http.HandlerFunc(s.postUsage)))
	return s.observe(securityHeaders(mux))
}

// observe logs one structured line per request with a request ID, status and latency.
// Query strings are deliberately not logged: they can contain search terms (PII).
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
		s.Log.Info("http_request",
			"request_id", id, "method", r.Method, "route", r.Pattern, "path", r.URL.Path,
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

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Store.Summary(r.Context(), s.UserID, s.Now(), 14)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	stage := q.Get("stage")
	if stage != "" && !store.ValidStage(stage) {
		writeErr(w, http.StatusBadRequest, "invalid stage")
		return
	}
	if len(q.Get("q")) > 200 {
		writeErr(w, http.StatusBadRequest, "query too long")
		return
	}
	apps, err := s.Store.ListApplications(r.Context(), s.UserID, store.ListFilter{Stage: stage, Query: q.Get("q")})
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"applications": apps})
}

func (s *Server) getApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	app, events, err := s.Store.GetApplication(r.Context(), s.UserID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"application": app, "events": events})
}

func (s *Server) patchApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Stage *string `json:"stage"` // "" clears a manual override
		store.ApplicationEdit
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Stage != nil && *body.Stage != "" && !store.ValidStage(*body.Stage) {
		writeErr(w, http.StatusBadRequest, "invalid stage")
		return
	}
	if err := body.ApplicationEdit.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Stage != nil {
		if err := s.Store.SetStage(r.Context(), s.UserID, id, *body.Stage); errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		} else if err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	if body.ApplicationEdit.Empty() {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	newID, err := s.Store.EditApplication(r.Context(), s.UserID, id, body.ApplicationEdit)
	s.writeResult(w, r, err, http.StatusOK, map[string]int64{"id": newID})
}

func (s *Server) getIntent(w http.ResponseWriter, r *http.Request) {
	i, err := s.Store.GetIntent(r.Context(), s.UserID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, i)
}

func (s *Server) putIntent(w http.ResponseWriter, r *http.Request) {
	var i store.Intent
	if !decode(w, r, &i) {
		return
	}
	if err := i.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.PutIntent(r.Context(), s.UserID, i, s.Now()); err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, i)
}

func (s *Server) ingest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []store.IngestEvent `json:"events"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Events) > 500 {
		writeErr(w, http.StatusBadRequest, "at most 500 events per batch")
		return
	}
	for idx, e := range body.Events {
		if err := e.Validate(); err != nil {
			writeErr(w, http.StatusBadRequest, "event "+strconv.Itoa(idx)+": "+err.Error())
			return
		}
	}
	added, err := s.Store.Ingest(r.Context(), s.UserID, body.Events)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.Log.Info("events_ingested", "received", len(body.Events), "added", added)
	writeJSON(w, http.StatusOK, map[string]int{"received": len(body.Events), "added": added})
}

func (s *Server) analysis(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Store.Analysis(r.Context(), s.UserID, s.Now())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) detail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.Store.Detail(r.Context(), s.UserID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	} else if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	p, err := s.Store.GetProfile(r.Context(), s.UserID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	var p store.Profile
	if !decode(w, r, &p) {
		return
	}
	p.UpdatedAt = nil
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.PutProfile(r.Context(), s.UserID, p, s.Now()); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.getProfile(w, r)
}

// writeResult maps store errors for the internal write endpoints.
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

func (s *Server) putPosting(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var p store.Posting
	if !decode(w, r, &p) {
		return
	}
	if err := p.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SavePosting(r.Context(), s.UserID, id, p, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) postFit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var f store.Fit
	if !decode(w, r, &f) {
		return
	}
	if err := f.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, s.Store.SaveFit(r.Context(), s.UserID, id, f, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) postOutreach(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var o store.OutreachDraft
	if !decode(w, r, &o) {
		return
	}
	if o.Subject == "" || o.Body == "" || o.Model == "" {
		writeErr(w, http.StatusBadRequest, "subject, body and model are required")
		return
	}
	did, err := s.Store.SaveOutreach(r.Context(), s.UserID, id, o, s.Now())
	if errors.Is(err, store.ErrBadContact) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, err, http.StatusCreated, map[string]int64{"id": did})
}

func (s *Server) postUsage(w http.ResponseWriter, r *http.Request) {
	var u store.LLMUsage
	if !decode(w, r, &u) {
		return
	}
	if u.Purpose == "" || u.Model == "" || u.InputTokens < 0 || u.OutputTokens < 0 {
		writeErr(w, http.StatusBadRequest, "invalid usage record")
		return
	}
	s.writeResult(w, r, s.Store.RecordUsage(r.Context(), s.UserID, u, s.Now()), http.StatusNoContent, nil)
}

func (s *Server) organizations(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Store.Organizations(r.Context(), s.UserID, s.Now())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) organization(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	d, err := s.Store.Organization(r.Context(), s.UserID, id, s.Now())
	s.writeResult(w, r, err, http.StatusOK, d)
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

func (s *Server) mailboxes(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.Mailboxes(r.Context(), s.UserID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	st, err := s.Store.GetSyncSettings(r.Context(), s.UserID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mailboxes": m, "settings": st})
}

func (s *Server) addMailbox(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Address string `json:"address"`
	}
	if !decode(w, r, &body) {
		return
	}
	m, err := s.Store.AddMailbox(r.Context(), s.UserID, body.Address, s.Now())
	if errors.Is(err, store.ErrBadAddress) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.writeResult(w, r, err, http.StatusCreated, m)
}

func (s *Server) patchMailbox(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.writeResult(w, r, s.Store.SetMailboxEnabled(r.Context(), s.UserID, id, body.Enabled), http.StatusNoContent, nil)
}

func (s *Server) removeMailbox(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	s.writeResult(w, r, s.Store.RemoveMailbox(r.Context(), s.UserID, id), http.StatusNoContent, nil)
}

func (s *Server) mailboxState(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var st store.MailboxState
	if !decode(w, r, &st) {
		return
	}
	s.writeResult(w, r, s.Store.SetMailboxState(r.Context(), s.UserID, id, st), http.StatusNoContent, nil)
}

func (s *Server) putSyncSettings(w http.ResponseWriter, r *http.Request) {
	var st store.SyncSettings
	if !decode(w, r, &st) {
		return
	}
	if err := st.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if st.Lookback != "custom" {
		st.SinceDate = ""
	}
	s.writeResult(w, r, s.Store.PutSyncSettings(r.Context(), s.UserID, st), http.StatusOK, st)
}

// maintenance runs after every sync: free repairs, then the refresh is logged
// for the morning digest. It returns the refresh id.
func (s *Server) maintenance(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Maintain(r.Context()); err != nil {
		s.internalErr(w, r, err)
		return
	}
	id, err := s.Store.RecordRefresh(r.Context(), s.UserID, s.Now())
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"refresh_id": id})
}

func (s *Server) digest(w http.ResponseWriter, r *http.Request) {
	d, err := s.Store.Digest(r.Context(), s.UserID, s.Now())
	s.writeResult(w, r, err, http.StatusOK, d)
}

func (s *Server) putDigestText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshID int64  `json:"refresh_id"`
		Text      string `json:"text"`
		Model     string `json:"model"`
	}
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" || len(body.Text) > 2000 || body.Model == "" {
		writeErr(w, http.StatusBadRequest, "text and model are required")
		return
	}
	err := s.Store.SaveDigestText(r.Context(), s.UserID, body.RefreshID, body.Text, body.Model, s.Now())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusConflict, "not the latest refresh")
		return
	}
	s.writeResult(w, r, err, http.StatusNoContent, nil)
}

// requestResearch queues web research for Claude in the app (see store/research.go).
func (s *Server) requestResearch(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(r)
		if !ok {
			writeErr(w, http.StatusBadRequest, "invalid id")
			return
		}
		at, err := s.Store.RequestResearch(r.Context(), s.UserID, kind, id, s.Now())
		s.writeResult(w, r, err, http.StatusAccepted, map[string]any{"research_requested_at": at})
	}
}

func (s *Server) pendingResearch(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.PendingResearch(r.Context(), s.UserID)
	s.writeResult(w, r, err, http.StatusOK, map[string]any{"requests": list})
}
