// agent runs analysis on request: a rule-based fit score per application with a
// short Claude-written summary, company diagnoses, and cold-email drafts. It never sends anything; results go to core-api.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ayush3923/project-dzinga/services/agent/internal/analyze"
	"github.com/ayush3923/project-dzinga/services/agent/internal/core"
	"github.com/ayush3923/project-dzinga/services/agent/internal/llm"
	"github.com/ayush3923/project-dzinga/services/agent/internal/router"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "agent")
	token := os.Getenv("DZ_INTERNAL_TOKEN")
	key := os.Getenv("ANTHROPIC_API_KEY")
	if len(token) < 32 || key == "" {
		log.Error("DZ_INTERNAL_TOKEN and ANTHROPIC_API_KEY must be set")
		os.Exit(1)
	}
	routes, err := router.FromEnv()
	if err != nil {
		log.Error("invalid model routes", "err", err)
		os.Exit(1)
	}
	a := &analyze.Agent{
		Core:   core.New(env("DZ_CORE_URL", "http://127.0.0.1:8081"), token),
		Router: &router.Router{LLM: llm.NewClaude(key), Routes: routes, Log: log},
		Log:    log,
		Fetch:  analyze.NewFetch(),
	}
	srv := &http.Server{
		Addr:              env("DZ_AGENT_ADDR", "127.0.0.1:8082"), // loopback only
		Handler:           Handler(a, token, log),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      analyze.Timeout + 30*time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		for _, t := range router.Tasks {
			log.Info("route", "task", t, "model", routes[t].Model, "effort", routes[t].Effort, "escalate", routes[t].Escalate)
		}
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(sctx)
}

// Handler exposes the agent's two actions to the web app's server.
func Handler(a *analyze.Agent, token string, log *slog.Logger) http.Handler {
	busy := make(chan struct{}, 2) // at most two analyses at once: protects the API budget
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	run := func(w http.ResponseWriter, r *http.Request, f func(ctx context.Context, id int64) (any, error)) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var id int64 // 0 for routes without one (the digest)
		if v := r.PathValue("id"); v != "" {
			var err error
			if id, err = strconv.ParseInt(v, 10, 64); err != nil || id <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
				return
			}
		}
		select {
		case busy <- struct{}{}:
			defer func() { <-busy }()
		default:
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "two analyses are already running; try again shortly"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), analyze.Timeout)
		defer cancel()
		start := time.Now()
		out, err := f(ctx, id)
		log.Info("analysis", "route", r.Pattern, "application_id", id, "ok", err == nil, "duration_ms", time.Since(start).Milliseconds())
		switch {
		case errors.Is(err, core.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "application or contact not found"})
		case errors.Is(err, analyze.ErrNoProfile), errors.Is(err, analyze.ErrNoPosting):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, llm.ErrRefused):
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		case err != nil:
			log.Error("analysis_failed", "application_id", id, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "analysis failed: " + err.Error()})
		default:
			writeJSON(w, http.StatusOK, out)
		}
	}
	mux.HandleFunc("POST /v1/applications/{id}/fit", func(w http.ResponseWriter, r *http.Request) {
		run(w, r, func(ctx context.Context, id int64) (any, error) { return a.Fit(ctx, id) })
	})
	mux.HandleFunc("POST /v1/applications/{id}/outreach", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ContactID *int64 `json:"contact_id"`
		}
		if r.ContentLength > 0 {
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
				return
			}
		}
		run(w, r, func(ctx context.Context, id int64) (any, error) { return a.Outreach(ctx, id, body.ContactID) })
	})
	mux.HandleFunc("POST /v1/digest", func(w http.ResponseWriter, r *http.Request) {
		run(w, r, func(ctx context.Context, _ int64) (any, error) { return a.Digest(ctx) })
	})
	mux.HandleFunc("POST /v1/organizations/{id}/diagnosis", func(w http.ResponseWriter, r *http.Request) {
		run(w, r, func(ctx context.Context, id int64) (any, error) { return a.Diagnose(ctx, id) })
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
