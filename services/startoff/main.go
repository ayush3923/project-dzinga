// The startoff service (ADR 0011): runs Dzinga's daily jobs at 07:00 (refresh mail,
// request the day's quiz, queue organization diagnoses for Claude) and owns company
// research with its full history: profiles, quiz facts and news, diagnoses, and
// Claude's research queue. It calls the organizations, sync and quiz services;
// none of them calls it.
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
	_ "time/tzdata" // the image has no zoneinfo

	"github.com/ayush3923/project-dzinga/services/startoff/internal/api"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/jobs"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/peers"
	"github.com/ayush3923/project-dzinga/services/startoff/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	importOrgs := flag.String("import-orgs", "", "copy research from this organizations database (at v2; once, into an empty database), then exit")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "startoff")
	fail := func(msg string, args ...any) { log.Error(msg, args...); os.Exit(1) }

	st, err := store.Open(env("DZ_STARTOFF_DB_PATH", "../../data/startoff.db"))
	if err != nil {
		fail("open store", "err", err)
	}
	defer st.Close()
	if *importOrgs != "" {
		n, err := st.ImportFromOrgs(context.Background(), *importOrgs)
		if err != nil {
			fail("import", "err", err)
		}
		fmt.Printf("imported research for %d organizations\n", n)
		return
	}

	token := os.Getenv("DZ_INTERNAL_TOKEN")
	if len(token) < 32 {
		fail("DZ_INTERNAL_TOKEN must be set (at least 32 characters)")
	}
	loc, err := time.LoadLocation(env("DZ_TIMEZONE", "Asia/Kolkata"))
	if err != nil {
		fail("DZ_TIMEZONE", "err", err)
	}
	at, err := time.Parse("15:04", env("DZ_DAILY_AT", "07:00"))
	if err != nil {
		fail("DZ_DAILY_AT must be HH:MM", "err", err)
	}
	user := env("DZ_USER_ID", "owner")
	orgs := peers.NewOrgs(env("DZ_ORGS_URL", "http://127.0.0.1:8085"), token)
	runner := &jobs.Runner{
		Store: st, Orgs: orgs, Log: log, UserID: user, Now: time.Now, Loc: loc,
		Ingest:      peers.NewIngest(env("DZ_INGEST_URL", "http://127.0.0.1:8083"), token),
		Quiz:        peers.NewQuiz(env("DZ_QUIZ_URL", "http://127.0.0.1:8084"), token),
		At:          time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute,
		Poll:        3 * time.Second,
		SyncTimeout: 20 * time.Minute,
	}
	if err := st.CloseInterrupted(context.Background(), time.Now()); err != nil {
		fail("close interrupted runs", "err", err)
	}
	srv := &http.Server{
		// Loopback only locally; in the cluster, NetworkPolicy limits callers.
		Addr: env("DZ_STARTOFF_ADDR", "127.0.0.1:8086"),
		Handler: (&api.Server{
			Store: st, Orgs: orgs, Runner: runner, Log: log, InternalToken: token, UserID: user, Now: time.Now,
			Background: func(f func()) { go f() },
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if env("DZ_SCHEDULE", "on") == "on" {
		go runner.Schedule(ctx)
		log.Info("daily run scheduled", "at", env("DZ_DAILY_AT", "07:00"), "tz", loc.String())
	}
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
