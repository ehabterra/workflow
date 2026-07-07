// Command timer_escalation demonstrates the host-driven timer model (roadmap M4):
// the library models time, the host owns the clock.
//
// It builds an approval workflow with a 3-day escalation (`after: 72h`), persists
// a fleet of instances at different ages into SQLite, then runs a "cron tick" —
// ListDue + FireDue — against a fixed, hand-advanced clock so the whole thing runs
// instantly and deterministically (no sleeping, no wall-clock).
//
// Run it with:
//
//	go run ./examples/timer_escalation
package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	wfyaml "github.com/ehabterra/workflow/yaml"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed workflow.yaml
var workflowYAML []byte

const day = 24 * time.Hour

// start is the reference "now" of the very first cron tick. Every instance is
// seeded relative to it, and later ticks advance a fake clock from it — so the
// example is fully deterministic and never sleeps.
var start = time.Date(2026, 1, 8, 9, 0, 0, 0, time.UTC)

// at returns a constant clock — the host's authoritative time for one evaluation.
// The Due API always takes `now` as a parameter, so a fixed clock makes timer
// behaviour reproducible in tests and demos alike.
func at(t time.Time) func() time.Time { return func() time.Time { return t } }

func main() {
	ctx := context.Background()

	// --- 1. Storage: a self-contained SQLite database in a temp file. ---
	dbFile, err := os.CreateTemp("", "timer_escalation-*.db")
	if err != nil {
		log.Fatalf("create temp db: %v", err)
	}
	_ = dbFile.Close()
	defer os.Remove(dbFile.Name())

	db, err := sql.Open("sqlite3", dbFile.Name())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		log.Fatalf("new storage: %v", err)
	}
	// EnsureSchema creates the state table and the M4 due-index column/index that
	// ListDue scans. It is idempotent, so it is safe to call on every startup.
	if err := store.EnsureSchema(ctx); err != nil {
		log.Fatalf("ensure schema: %v", err)
	}

	// A DueStorage backend + WithoutRegistryCache is the multi-replica-safe setup:
	// every load reads fresh state, and FireDue saves under optimistic concurrency,
	// so several cron hosts can scan the same fleet without clobbering each other.
	mgr := workflow.NewManager(workflow.NewRegistry(), store)

	// --- 2. Definition: loaded from YAML so `after: 72h` is on show. ---
	def := loadDefinition()

	// --- 3. Seed a fleet of persisted instances at different ages. ---
	// Deadline = entry time + 72h. Ages are stated relative to the first tick.
	seedSubmitted(ctx, mgr, def, "req-101-fresh", start.Add(-1*day))   // 1 day old  -> due in 2 days
	seedSubmitted(ctx, mgr, def, "req-102-aging", start.Add(-2*day))   // 2 days old -> due in 1 day
	seedSubmitted(ctx, mgr, def, "req-103-stale", start.Add(-4*day))   // 4 days old -> already overdue
	seedSubmitted(ctx, mgr, def, "req-104-ancient", start.Add(-9*day)) // 9 days old -> already overdue

	// req-105 was submitted just as long ago as the stale one, but a human approved
	// it. Approval leaves `submitted`, so no timer is running: it never appears in
	// ListDue and is never escalated. Business action always wins over the clock.
	seedSubmitted(ctx, mgr, def, "req-105-approved", start.Add(-4*day))
	if err := mgr.Execute(ctx, "req-105-approved", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransition("approve")
	}); err != nil {
		log.Fatalf("approve req-105: %v", err)
	}

	fmt.Println("Fleet seeded (deadline = submitted + 72h):")
	fmt.Println("  req-101-fresh     submitted 1 day  ago")
	fmt.Println("  req-102-aging     submitted 2 days ago")
	fmt.Println("  req-103-stale     submitted 4 days ago")
	fmt.Println("  req-104-ancient   submitted 9 days ago")
	fmt.Println("  req-105-approved  submitted 4 days ago, then APPROVED (timer gone)")

	// --- 4. Run the cron, advancing a fake clock day by day. ---
	// A real deployment calls tick() from cron/a ticker with time.Now(); here we
	// hand it a fixed, advancing clock so the run is instant and reproducible.
	for _, offset := range []time.Duration{0, 1 * day, 2 * day} {
		tick(ctx, mgr, def, start.Add(offset))
	}

	fmt.Println("\nNothing left overdue — the fleet has settled. Nothing was scheduled")
	fmt.Println("internally: every escalation happened because the host asked \"what is")
	fmt.Println("due as of now?\" and fired it. A crash between ticks loses nothing.")
}

// tick is the whole host-driven cron: find everything overdue as of `now`, then
// advance each instance. It is the ~10 lines a real deployment schedules.
func tick(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, now time.Time) {
	fmt.Printf("\n=== cron tick @ %s ===\n", now.Format("2006-01-02 15:04"))

	dueIDs, err := mgr.ListDue(ctx, now, 0) // 0 = no limit; page by raising `now`/limit
	if err != nil {
		log.Fatalf("ListDue: %v", err)
	}
	if len(dueIDs) == 0 {
		fmt.Println("  nothing due")
		return
	}
	fmt.Printf("  ListDue reports overdue: %v\n", dueIDs)
	for _, id := range dueIDs {
		fired, err := mgr.FireDue(ctx, id, def, now)
		if err != nil {
			log.Printf("  FireDue(%s): %v", id, err)
			continue
		}
		if len(fired) > 0 {
			fmt.Printf("  %-16s escalated (fired %v)\n", id, fired)
		}
	}
}

// seedSubmitted persists a fresh `submitted` instance whose token entered at
// entryTime, so its escalation deadline is deterministic. Pinning the stamping
// clock with WithClock is what makes the seeded ages exact.
func seedSubmitted(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, id string, entryTime time.Time) {
	wf, err := workflow.NewWorkflow(id, def, "submitted", workflow.WithClock(at(entryTime)))
	if err != nil {
		log.Fatalf("NewWorkflow(%s): %v", id, err)
	}
	wf.SetManager(mgr)
	if err := mgr.SaveWorkflow(ctx, id, wf); err != nil {
		log.Fatalf("SaveWorkflow(%s): %v", id, err)
	}
}

// loadDefinition builds the workflow definition from the embedded YAML, so the
// `after: 72h` timer is declared declaratively rather than in Go.
func loadDefinition() *workflow.Definition {
	config, err := wfyaml.LoadConfigFromBytes(workflowYAML)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	def, err := wfyaml.NewLoader().LoadDefinition(config)
	if err != nil {
		log.Fatalf("load definition: %v", err)
	}
	return def
}
