// Command expense_approval runs the M5 dogfood reference system: an
// expense-approval web service with parallel review, host-driven escalation
// timers, an audit trail, and a CPN batch payment run.
//
// Zero-setup demo (SQLite file next to the binary):
//
//	go run . -db expenses.db -escalate-after 2m -tick 10s
//	open http://localhost:8080
//
// Point it at Postgres instead with -postgres-dsn (or EXPENSE_POSTGRES_DSN).
// The demo has no authentication or CSRF protection — it is a reference
// system for the workflow library, not a deployable product.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	dbPath := flag.String("db", "expenses.db", "SQLite database file (ignored with -postgres-dsn)")
	postgresDSN := flag.String("postgres-dsn", os.Getenv("EXPENSE_POSTGRES_DSN"), "PostgreSQL DSN; empty = SQLite")
	escalateAfter := flag.Duration("escalate-after", 0, "override the 72h escalation deadline (e.g. 2m for a live demo)")
	tick := flag.Duration("tick", 10*time.Second, "escalation tick interval (the host-owned clock)")
	reconcileEvery := flag.Duration("reconcile", time.Minute, "periodic reconcile interval; 0 disables (manual button still works)")
	flag.Parse()

	// Shut down cleanly on Ctrl-C / SIGTERM: stop accepting requests,
	// finish in-flight ones, stop the tickers, close the database. State is
	// in the database, so a hard kill is also safe — this just exits tidily.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	driver, dsn := "sqlite3", *dbPath+"?_busy_timeout=5000&_journal_mode=WAL"
	if *postgresDSN != "" {
		driver, dsn = "pgx", *postgresDSN
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	app, err := NewApp(ctx, db, driver, *escalateAfter, time.Now)
	if err != nil {
		log.Fatalf("start app: %v", err)
	}

	// The host-driven timer loop (M4): the library models time, this ticker
	// owns the clock. Deadlines live in the database, so they survive
	// restarts. Loops are joined on shutdown so the deferred db.Close never
	// races an in-flight tick.
	var loops sync.WaitGroup
	loops.Add(1)
	go func() {
		defer loops.Done()
		every(ctx, *tick, func(now time.Time) {
			fired, err := app.Tick(ctx, now)
			if err != nil {
				log.Printf("tick: %v", err)
			}
			for id, names := range fired {
				log.Printf("tick: %s fired %v", id, names)
			}
		})
	}()

	// Periodic self-healing for the documented cross-instance crash
	// windows (see App.Reconcile).
	if *reconcileEvery > 0 {
		loops.Add(1)
		go func() {
			defer loops.Done()
			every(ctx, *reconcileEvery, func(time.Time) {
				rep, err := app.Reconcile(ctx)
				if err != nil {
					log.Printf("reconcile: %v", err)
				}
				if rep != nil && (rep.Enqueued+rep.Marked+rep.DraftsDeleted) > 0 {
					log.Printf("reconcile: %d enqueued, %d marked paid, %d stale draft(s) deleted",
						rep.Enqueued, rep.Marked, rep.DraftsDeleted)
				}
			})
		}()
	}

	handler, err := NewServer(app)
	if err != nil {
		log.Fatalf("build server: %v", err)
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	// The serve error feeds back into the graceful path (no log.Fatal in a
	// goroutine — that would skip the deferred db.Close and the loop join).
	serveErr := make(chan error, 1)
	go func() {
		log.Printf("expense approval listening on %s (driver=%s, tick=%s)", *addr, driver, *tick)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		log.Print("shutting down…")
	case err := <-serveErr:
		if err != nil {
			log.Printf("serve: %v — shutting down", err)
		}
	}
	stop() // stop the tick/reconcile loops
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	loops.Wait() // no tick/reconcile may touch the DB after this point
	log.Print("bye")
}

// every runs fn on the interval until the context ends.
func every(ctx context.Context, interval time.Duration, fn func(now time.Time)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			fn(now)
		}
	}
}
