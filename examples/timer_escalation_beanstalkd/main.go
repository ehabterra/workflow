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
//	                             [long-lived worker replicas]
//
// The database is the source of truth for *what* is due; Beanstalkd only decides
// *who* fires it. Because ListDue re-derives the due set on every tick, the
// broker need not be durable or exactly-once: a dropped job is simply re-listed
// next tick, and a duplicated or redelivered job is a no-op thanks to FireDue's
// optimistic concurrency. That is what lets a tiny, in-memory distributor like
// Beanstalkd be enough — and this demo *proves* both properties live:
//
//   - the first tick deliberately enqueues one instance TWICE (an overlapping
//     scan): one worker escalates it, the other finds nothing due and no-ops;
//   - one worker simulates a crash mid-job (reserved but never deleted): the
//     released job is picked up and completed by another worker.
//
// To keep the demo self-contained, one process plays every role — a singleton
// dispatcher plus a pool of long-lived competing workers, each on its own
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
	"encoding/json"
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
	// instance jobs onto and workers reserve from.
	tubeName = "timers"

	// workerCount is how many long-lived competing workers drain the tube. Each
	// stands in for a separate replica of your fire service; Beanstalkd hands
	// every job to exactly one of them at a time.
	workerCount = 3

	// reserveTimeout bounds how long a worker blocks waiting for a job before it
	// checks whether it has been asked to shut down.
	reserveTimeout = 250 * time.Millisecond

	// jobTTR (time-to-run) is Beanstalkd's dead-man's-switch: if a worker
	// reserves a job and dies before deleting it, Beanstalkd auto-releases the
	// job after this long so another worker can pick it up. Set it comfortably
	// above a FireDue's latency.
	jobTTR = 30 * time.Second

	// releaseDelay is how long a job stays invisible after a *real* FireDue
	// failure before another worker may reserve it again. (The simulated crash
	// below releases with zero delay so the demo stays instant.)
	releaseDelay = 10 * time.Second

	// drainPoll is how often the demo's tick checks whether the tube is empty.
	// Pure demo bookkeeping so ticks print in order — production workers just
	// run forever and no one waits for a "tick" to finish.
	drainPoll = 25 * time.Millisecond
)

// start is the reference "now" of the first cron tick. Every instance is seeded
// relative to it, and later ticks advance a fake clock from it — so the example
// is deterministic and never sleeps for real time.
var start = time.Date(2026, 1, 8, 9, 0, 0, 0, time.UTC)

// at returns a constant clock — the host's authoritative time for one evaluation.
func at(t time.Time) func() time.Time { return func() time.Time { return t } }

// job is the payload that travels through the tube. It carries the dispatcher's
// scan time alongside the instance ID: the worker fires *as of that time*, so
// firing is deterministic with respect to the scan decision even if the job sits
// in the queue for a while. (A deployment may instead call time.Now() in the
// worker; carrying the scan time is what makes this demo's fake clock work and
// is a sound production pattern in its own right.)
type job struct {
	ID  string    `json:"id"`
	Now time.Time `json:"now"`
}

// crashOnce makes exactly one worker, once in the whole run, "crash" after
// reserving req-103-stale — reserved but never deleted — to demonstrate that
// the job survives its worker and is completed by another.
var crashOnce atomic.Bool

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
	mgr := workflow.NewManager(workflow.NewRegistry(), store)

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

	// --- 4. Start the long-lived worker replicas. ---
	// In production these run forever in every replica, knowing nothing about
	// "ticks": they just reserve, fire, delete. They are started once, here,
	// before any work exists, and idle on reserve until the dispatcher puts jobs.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 1; w <= workerCount; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			runWorker(ctx, mgr, def, addr, worker, stop)
		}(w)
	}
	fmt.Printf("\nDistributor: beanstalkd @ %s, tube %q, %d long-lived competing workers\n", addr, tubeName, workerCount)

	// --- 5. Run the cron dispatcher, advancing a fake clock day by day. ---
	// Each tick: scan ListDue, put one job per due instance. The first tick also
	// double-enqueues the oldest instance to simulate an overlapping scan.
	for i, offset := range []time.Duration{0, 1 * day, 2 * day} {
		tick(ctx, mgr, addr, start.Add(offset), i == 0)
	}

	close(stop)
	wg.Wait()

	fmt.Println("\nNothing left overdue — the fleet has settled. The library never")
	fmt.Println("scheduled anything: the dispatcher asked \"what is due as of now?\" and")
	fmt.Println("Beanstalkd handed each answer to a worker. The duplicate delivery and")
	fmt.Println("the crashed worker changed nothing — FireDue's idempotency and the")
	fmt.Println("job release absorbed both.")
}

// tick is one pass of the singleton dispatcher: find everything overdue as of
// `now` and enqueue one job per instance. In production this runs on ONE replica
// (guard it with a leader lock or a single CronJob) while workers run everywhere.
func tick(ctx context.Context, mgr *workflow.Manager, addr string, now time.Time, withDuplicate bool) {
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

	dc := dial(addr)
	defer dc.Close()
	tube := &beanstalk.Tube{Conn: dc, Name: tubeName}
	put := func(id string) {
		body, err := json.Marshal(job{ID: id, Now: now})
		if err != nil {
			log.Fatalf("marshal job %s: %v", id, err)
		}
		// pri=0 (highest), delay=0 (ready now), ttr=jobTTR (auto-requeue on crash).
		if _, err := tube.Put(body, 0, 0, jobTTR); err != nil {
			log.Printf("  put(%s): %v", id, err)
		}
	}
	for _, id := range dueIDs {
		put(id)
	}
	if withDuplicate {
		// Simulate an overlapping scan (e.g. a slow previous tick, or a second
		// dispatcher racing before a leader lock lands): the same instance is
		// enqueued twice. Whichever worker runs second finds nothing due.
		put(dueIDs[0])
		fmt.Printf("  (enqueued %s TWICE to simulate an overlapping scan)\n", dueIDs[0])
	}

	// Demo bookkeeping only: wait for the workers to drain the tube so each
	// tick's output prints together. Production has no equivalent step.
	waitForDrain(tube)
}

// runWorker is one long-lived replica: reserve -> FireDue -> delete, forever
// (until stop closes). A worker knows nothing about ticks or scans; the job
// itself carries everything needed to fire.
func runWorker(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, addr string, worker int, stop <-chan struct{}) {
	conn := dial(addr)
	defer conn.Close()
	ts := beanstalk.NewTubeSet(conn, tubeName)

	for {
		jobID, body, err := ts.Reserve(reserveTimeout)
		if err != nil {
			if errors.Is(err, beanstalk.ErrTimeout) {
				select {
				case <-stop:
					return
				default:
					continue // idle: no work right now, keep listening.
				}
			}
			log.Printf("  [worker %d] reserve: %v", worker, err)
			return
		}

		var j job
		if err := json.Unmarshal(body, &j); err != nil {
			// A malformed job would be redelivered forever; drop it instead.
			log.Printf("  [worker %d] bad job %d (%q): %v", worker, jobID, body, err)
			_ = conn.Delete(jobID)
			continue
		}

		// Demo: exactly once in the run, the worker holding req-103-stale
		// "crashes" — it reserved the job but never fires or deletes it. Releasing
		// with zero delay stands in for a TTR expiry (which would take jobTTR of
		// real waiting): either way the job returns to ready and another worker
		// completes it. The instance is NOT lost with its worker.
		if j.ID == "req-103-stale" && crashOnce.CompareAndSwap(false, true) {
			fmt.Printf("  [worker %d] %-16s reserved... simulated CRASH mid-job (job survives, will be redelivered)\n", worker, j.ID)
			_ = conn.Release(jobID, 0, 0)
			continue // the demo worker lives on; only this job's handling died.
		}

		fired, err := mgr.FireDue(ctx, j.ID, def, j.Now)
		if err != nil {
			// Leave the job for another attempt; the next cron tick would also
			// re-list it (the due row is unchanged), and FireDue is idempotent.
			log.Printf("  [worker %d] FireDue(%s): %v", worker, j.ID, err)
			_ = conn.Release(jobID, 0, releaseDelay)
			continue
		}
		if len(fired) > 0 {
			fmt.Printf("  [worker %d] %-16s escalated (fired %v)\n", worker, j.ID, fired)
		} else {
			// Nothing was due: this was the duplicate/redelivered job and another
			// worker already escalated the instance. FireDue no-ops — correctness
			// never depended on the broker delivering exactly once.
			fmt.Printf("  [worker %d] %-16s nothing due — duplicate delivery, no-op\n", worker, j.ID)
		}
		_ = conn.Delete(jobID)
	}
}

// waitForDrain polls the tube until no job is ready, reserved, or delayed —
// i.e. the workers have finished everything this tick enqueued. Demo-only.
func waitForDrain(tube *beanstalk.Tube) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		stats, err := tube.Stats()
		if err != nil {
			return // tube vanished (no jobs, no watchers): drained.
		}
		if stats["current-jobs-ready"] == "0" &&
			stats["current-jobs-reserved"] == "0" &&
			stats["current-jobs-delayed"] == "0" {
			return
		}
		time.Sleep(drainPoll)
	}
	log.Print("  waitForDrain: timed out waiting for the tube to empty")
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
