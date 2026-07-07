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
package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"net/http"
	"os"
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
	flag.Parse()

	ctx := context.Background()

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
	// owns the clock. Kill the process and restart it — deadlines survive,
	// because they live in the database, not in goroutines.
	go func() {
		t := time.NewTicker(*tick)
		defer t.Stop()
		for now := range t.C {
			fired, err := app.Tick(ctx, now)
			if err != nil {
				log.Printf("tick: %v", err)
				continue
			}
			for id, names := range fired {
				log.Printf("tick: %s fired %v", id, names)
			}
		}
	}()

	srv, err := NewServer(app)
	if err != nil {
		log.Fatalf("build server: %v", err)
	}
	log.Printf("expense approval listening on %s (driver=%s, tick=%s)", *addr, driver, *tick)
	log.Fatal(http.ListenAndServe(*addr, srv))
}
