// The organizations service: every company the user has applied to or prefers,
// with statistics and the preferred list (ADR 0010). Applications stay in core-api;
// this service reads them to group by company. Company research is the startoff
// service's (ADR 0011).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayush3923/project-dzinga/services/orgs/internal/api"
	"github.com/ayush3923/project-dzinga/services/orgs/internal/core"
	"github.com/ayush3923/project-dzinga/services/orgs/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	importCore := flag.String("import-core", "", "copy organizations from this core-api database (once, into an empty database), then exit")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "orgs")

	st, err := store.Open(env("DZ_ORGS_DB_PATH", "../../data/orgs.db"))
	if err != nil {
		log.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if *importCore != "" {
		n, err := st.ImportFromCore(context.Background(), *importCore)
		if err != nil {
			log.Error("import", "err", err)
			os.Exit(1)
		}
		fmt.Printf("imported %d organizations\n", n)
		return
	}

	token := os.Getenv("DZ_INTERNAL_TOKEN")
	if len(token) < 32 {
		log.Error("DZ_INTERNAL_TOKEN must be set (at least 32 characters)")
		os.Exit(1)
	}
	srv := &http.Server{
		// Loopback only locally; in the cluster, NetworkPolicy limits callers.
		Addr: env("DZ_ORGS_ADDR", "127.0.0.1:8085"),
		Handler: (&api.Server{
			Store: st, Core: core.New(env("DZ_CORE_URL", "http://127.0.0.1:8081"), token),
			Log: log, InternalToken: token, UserID: env("DZ_USER_ID", "owner"), Now: time.Now,
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
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
