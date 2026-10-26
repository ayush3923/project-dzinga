// core-api owns applications, events and intent, and serves them over HTTP.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayush3923/project-dzinga/services/core-api/internal/api"
	"github.com/ayush3923/project-dzinga/services/core-api/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "core-api")

	token := os.Getenv("DZ_INTERNAL_TOKEN")
	if len(token) < 32 {
		log.Error("DZ_INTERNAL_TOKEN must be set (at least 32 characters)")
		os.Exit(1)
	}

	st, err := store.Open(env("DZ_DB_PATH", "../../data/dzinga.db"))
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if n, err := st.SplitRolelessApplications(context.Background()); err != nil {
		log.Error("split merged applications", "err", err)
		os.Exit(1)
	} else if n > 0 {
		log.Info("split_merged_applications", "created", n)
	}
	if n, err := st.MergeOrphanApplications(context.Background()); err != nil {
		log.Error("merge orphan replies", "err", err)
		os.Exit(1)
	} else if n > 0 {
		log.Info("merged_orphan_replies", "merged", n)
	}
	if err := st.SeedMailboxes(context.Background()); err != nil {
		log.Error("seed mailboxes", "err", err)
		os.Exit(1)
	}
	if err := st.LinkAllOrganizations(context.Background()); err != nil {
		log.Error("link organizations", "err", err)
		os.Exit(1)
	}
	// Stage history is derived data: rebuild it so rule changes apply to old emails too.
	if err := st.RebuildAllHistory(context.Background()); err != nil {
		log.Error("rebuild stage history", "err", err)
		os.Exit(1)
	}

	srv := &http.Server{
		// Loopback only: the prototype is never reachable from the network.
		Addr: env("DZ_CORE_ADDR", "127.0.0.1:8081"),
		Handler: (&api.Server{
			Store: st, Log: log, InternalToken: token,
			UserID: env("DZ_USER_ID", "owner"), Now: time.Now,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
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
