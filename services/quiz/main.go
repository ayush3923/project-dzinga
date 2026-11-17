// The quiz service: the daily quiz (five multiple-choice questions, one written,
// one spoken), answers and feedback. Claude builds quizzes and writes feedback
// through the internal API (docs/runbooks/claude-queue.md, ADR 0008).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ayush3923/project-dzinga/services/quiz/internal/api"
	"github.com/ayush3923/project-dzinga/services/quiz/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "quiz")

	token := os.Getenv("DZ_INTERNAL_TOKEN")
	if len(token) < 32 {
		log.Error("DZ_INTERNAL_TOKEN must be set (at least 32 characters)")
		os.Exit(1)
	}
	dbPath := env("DZ_QUIZ_DB_PATH", "../../data/quiz.db")
	st, err := store.Open(dbPath)
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	srv := &http.Server{
		// Loopback only locally; in the cluster, NetworkPolicy limits callers.
		Addr: env("DZ_QUIZ_ADDR", "127.0.0.1:8084"),
		Handler: (&api.Server{
			Store: st, Log: log, InternalToken: token, UserID: env("DZ_USER_ID", "owner"), Now: time.Now,
			AudioDir: env("DZ_AUDIO_DIR", filepath.Join(filepath.Dir(dbPath), "quiz-audio")),
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second, // uploads of up to 8 MB
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
}
