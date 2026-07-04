// Command timer_escalation_beanstalkd shows how to drive the host-driven timer
// model (roadmap M4) across multiple worker replicas using Beanstalkd purely as
// a job distributor. The library still owns no clock and no scheduler — it only
// answers "what is due as of now?" (ListDue) and "fire it" (FireDue). Beanstalkd
// sits between those two calls and fans the work out to whichever worker is free.
//
// Architecture (see README.md for the full rationale):
//
//	ListDue (SQLite/Postgres) -> dispatcher (singleton) -> "timers" tube
//	                                                             |
//	                             reserve (competing) <-----------+
//	                             FireDue + delete
//	                             [worker replicas]
//
// The database is the source of truth for *what* is due; Beanstalkd only decides
// *who* fires it, so no two workers ever touch the same instance. Because ListDue
// re-derives the due set on every tick, the broker need not be durable or
// exactly-once: a dropped job is simply re-listed next tick, and a duplicated job
// is a no-op thanks to FireDue's optimistic concurrency. That is what lets a
// tiny, in-memory distributor like Beanstalkd be enough.
//
// To keep the demo self-contained, one process plays every role — a single
// dispatcher plus a pool of competing worker goroutines, each on its own
// Beanstalkd connection (stand-ins for separate replicas) — against a real
// Beanstalkd you start first:
//
//	docker compose up -d      # start beanstalkd on :11300
//	go run .                  # seed a fleet and drive the cron
//
// Set BEANSTALKD_ADDR to point at a different host:port.
package main

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	wfyaml "github.com/ehabterra/workflow/yaml"

	"github.com/beanstalkd/go-beanstalk"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed workflow.yaml
var workflowYAML []byte

const (
	day = 24 * time.Hour

	// tubeName is the Beanstalkd tube (named queue) the dispatcher puts due
	// instance IDs onto and workers reserve from.
	tubeName = "timers"

	// workerCount is how many competing worker goroutines drain the tube. Each
	// stands in for a separate replica of your fire service; Beanstalkd hands
	// every job to exactly one of them.
	workerCount = 3

	// reserveTimeout bounds how long a worker blocks waiting for a job. When it
	// elapses with the tube already drained, the worker knows the tick is done.
	reserveTimeout = 1 * time.Second

	// jobTTR (time-to-run) is Beanstalkd's dead-man's-switch: if a worker
	// reserves a job and dies before deleting it, Beanstalkd auto-releases the
	// job after this long so another worker can pick it up. Set it comfortably
	// above a FireDue's latency.
	jobTTR = 30 * time.Second
)

// start is the reference "now" of the first cron tick. Every instance is seeded
// relative to it, and later ticks advance a fake clock from it — so the example
// is deterministic and never sleeps for real time.
var start = time.Date(2026, 1, 8, 9, 0, 0, 0, time.UTC)

// at returns a constant clock — the host's authoritative time for one evaluation.
func at(t time.Time) func() time.Time { return func() time.Time { return t } }

func main() {
	ctx := context.Background()

	addr := os.Getenv("BEANSTALKD_ADDR")
	if addr == "" {
		addr = "127.0.0.1:11300"
	}

	// Fail early with a helpful hint if the distributor isn't up.
	probe, err := beanstalk.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("cannot reach beanstalkd at %s: %v\n\nStart it first:\n\tdocker compose up -d\n(or set BEANSTALKD_ADDR)", addr, err)
	}
	_ = probe.Close()

	// --- 1. Storage: a self-contained SQLite database in a temp file. ---
	dbFile, err := os.CreateTemp("", "timer_escalation_beanstalkd-*.db")
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
	// SQLite is single-writer. Serializing DB access keeps this concurrent demo
	// free of "database is locked" flakes; a real multi-replica deployment uses
	// the Postgres backend, which handles concurrent writers natively.
	db.SetMaxOpenConns(1)

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
	// every load reads fresh state, and FireDue saves under optimistic concurrency.
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())

	// --- 2. Definition: loaded from YAML so `after: 72h` is on show. ---
	def := loadDefinition()

	// --- 3. Seed a fleet of persisted instances at different ages. ---
	// Deadline = entry time + 72h. Ages are stated relative to the first tick.
	seedSubmitted(ctx, mgr, def, "req-101-fresh", start.Add(-1*day))   // 1 day old  -> due in 2 days
	seedSubmitted(ctx, mgr, def, "req-102-aging", start.Add(-2*day))   // 2 days old -> due in 1 day
	seedSubmitted(ctx, mgr, def, "req-103-stale", start.Add(-4*day))   // 4 days old -> already overdue
	seedSubmitted(ctx, mgr, def, "req-104-ancient", start.Add(-9*day)) // 9 days old -> already overdue

	// req-105 was submitted as long ago as the stale one, but a human approved it.
	// Approval leaves `submitted`, so no timer runs: it never appears in ListDue.
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
	fmt.Printf("\nDistributor: beanstalkd @ %s, tube %q, %d competing workers\n", addr, tubeName, workerCount)

	// --- 4. Run the cron, advancing a fake clock day by day. ---
	// Each tick: the (singleton) dispatcher scans ListDue and puts due IDs onto
	// the tube; the worker pool competes to reserve, FireDue, and delete them.
	for _, offset := range []time.Duration{0, 1 * day, 2 * day} {
		tick(ctx, mgr, def, addr, start.Add(offset))
	}

	fmt.Println("\nNothing left overdue — the fleet has settled. The library never")
	fmt.Println("scheduled anything: the dispatcher asked \"what is due as of now?\" and")
	fmt.Println("Beanstalkd handed each answer to exactly one worker.")
}

// tick is one host-driven cron pass. The dispatcher (this goroutine) is the
// singleton scanner; the worker pool is the set of replicas. In production the
// dispatcher runs on ONE replica (guard it with a leader lock or a single
// CronJob) while workers run everywhere.
func tick(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, addr string, now time.Time) {
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

	// Dispatcher: put each due ID onto the tube on its own connection.
	dc := dial(addr)
	defer dc.Close()
	tube := &beanstalk.Tube{Conn: dc, Name: tubeName}
	for _, id := range dueIDs {
		// pri=0 (highest), delay=0 (ready now), ttr=jobTTR (auto-requeue on crash).
		if _, err := tube.Put([]byte(id), 0, 0, jobTTR); err != nil {
			log.Printf("  put(%s): %v", id, err)
		}
	}

	// Workers: compete to drain the tube. Each dials its own connection because a
	// Beanstalkd job may only be deleted by the connection that reserved it.
	var wg sync.WaitGroup
	var fired int64
	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			runWorker(ctx, mgr, def, addr, now, worker, &fired)
		}(w)
	}
	wg.Wait()

	fmt.Printf("  tick done: %d escalated\n", atomic.LoadInt64(&fired))
}

// runWorker reserves jobs until the tube is drained (a reserve timeout with no
// ready job), firing each due instance and deleting the job on success.
func runWorker(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, addr string, now time.Time, worker int, fired *int64) {
	conn := dial(addr)
	defer conn.Close()
	ts := beanstalk.NewTubeSet(conn, tubeName)

	for {
		jobID, body, err := ts.Reserve(reserveTimeout)
		if err != nil {
			if errors.Is(err, beanstalk.ErrTimeout) {
				return // tube drained: this tick is done for us.
			}
			log.Printf("  [worker %d] reserve: %v", worker, err)
			return
		}
		id := string(body)

		firedNames, err := mgr.FireDue(ctx, id, def, now)
		if err != nil {
			// Leave the job for another attempt; the next cron tick would also
			// re-list it (the due row is unchanged), and FireDue is idempotent.
			log.Printf("  [worker %d] FireDue(%s): %v", worker, id, err)
			_ = conn.Release(jobID, 0, 10*time.Second)
			continue
		}
		_ = conn.Delete(jobID)
		if len(firedNames) > 0 {
			atomic.AddInt64(fired, 1)
			fmt.Printf("  [worker %d] %-16s escalated (fired %v)\n", worker, id, firedNames)
		}
	}
}

// dial opens a Beanstalkd connection or dies trying. Connections are stateful
// (a reserved job is bound to its connection), so every role gets its own.
func dial(addr string) *beanstalk.Conn {
	conn, err := beanstalk.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("dial beanstalkd %s: %v", addr, err)
	}
	return conn
}

// seedSubmitted persists a fresh `submitted` instance whose token entered at
// entryTime, so its escalation deadline is deterministic.
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
