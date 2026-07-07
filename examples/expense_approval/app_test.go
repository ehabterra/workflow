package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
)

// newTestApp opens a fresh SQLite-backed app in a temp dir. The returned db
// is shared so tests can reopen a "restarted" app over the same file.
func newTestApp(t *testing.T) (*App, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "expenses.db")
	db, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	app, err := NewApp(context.Background(), db, "sqlite3", 0, time.Now)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app, db
}

func mustSubmit(t *testing.T, app *App, submitter string, amount float64) string {
	t.Helper()
	id, err := app.SubmitExpense(context.Background(), submitter, "test expense", amount)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return id
}

// TestEscalationTick drives the M4 host-driven timer path: nothing is due
// now; 73 hours later both review branches escalate; the escalated branch is
// still approvable. No sleeping — the tick just receives a future now.
func TestEscalationTick(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "alice", 120)

	fired, err := app.Tick(ctx, time.Now())
	if err != nil {
		t.Fatalf("tick(now): %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("nothing should be due yet, fired %v", fired)
	}

	later := time.Now().Add(73 * time.Hour)
	fired, err = app.Tick(ctx, later)
	if err != nil {
		t.Fatalf("tick(+73h): %v", err)
	}
	names := fired[id]
	if len(names) != 2 {
		t.Fatalf("want both escalations for %s, got %v", id, fired)
	}

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("escalated_legal") || !view.Has("escalated_finance") {
		t.Fatalf("want escalated branches, marking %v", view.Places)
	}

	// A second tick at the same instant must be a no-op (the due index
	// self-heals; no re-fire, no version churn loops).
	fired, err = app.Tick(ctx, later)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("second tick should fire nothing, got %v", fired)
	}

	// The escalated branch is still approvable, via the *_escalated
	// transition.
	names, err = app.Approve(ctx, id, "legal", "lawyer")
	if err != nil {
		t.Fatalf("approve escalated: %v", err)
	}
	if names[0] != "legal_approve_escalated" {
		t.Fatalf("want legal_approve_escalated, fired %v", names)
	}

	// Timer firings are recorded in the audit trail (post-hoc, actor
	// "timer").
	recs, err := app.hist.ListHistory(ctx, id, history.QueryOptions{Actor: "timer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 timer history records, got %d", len(recs))
	}
}

// TestCrashConsistency proves the M3.5 atomicity claim through the app's own
// plumbing: if the history side effect fails, the state save rolls back with
// it — the reloaded instance is untouched and the audit trail has no record.
func TestCrashConsistency(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "bob", 300)

	boom := errors.New("history write crashed")
	err := app.mgr.Execute(ctx, id, app.expenseDef, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "legal_approve")
	}, workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
		return boom
	}))
	if !errors.Is(err, boom) {
		t.Fatalf("want the side-effect error, got %v", err)
	}

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("pending_legal") {
		t.Fatalf("state must roll back with the failed side effect, marking %v", view.Places)
	}
	recs, err := app.hist.ListHistory(ctx, id, history.QueryOptions{Transition: "legal_approve"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 0 {
		t.Fatalf("no history record may exist for the rolled-back fire, got %d", len(recs))
	}
}

// TestConcurrentApprovals races legal and finance through Manager.Execute's
// optimistic-concurrency retry: both must land, and the AND-join must fire
// exactly once.
func TestConcurrentApprovals(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "carol", 800)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, branch := range []string{"legal", "finance"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = app.Approve(ctx, id, branch, branch+"-bot")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent approve %d: %v", i, err)
		}
	}

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("approved") {
		t.Fatalf("want approved after both branches, marking %v", view.Places)
	}
	recs, err := app.hist.ListHistory(ctx, id, history.QueryOptions{Transition: "finalize"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("finalize must fire exactly once, got %d records", len(recs))
	}
}

// TestRestartResumesFleet simulates a crash/restart: a second App over the
// same database sees the fleet, passes the definition-fingerprint check, and
// its timers still fire — state lives in the database, the clock in the
// host.
func TestRestartResumesFleet(t *testing.T) {
	app1, db := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app1, "dave", 250)

	app2, err := NewApp(ctx, db, "sqlite3", 0, time.Now)
	if err != nil {
		t.Fatalf("restart app: %v", err)
	}
	views, err := app2.ListExpenses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].ID != id {
		t.Fatalf("restarted app must see the fleet, got %+v", views)
	}
	fired, err := app2.Tick(ctx, time.Now().Add(80*time.Hour))
	if err != nil {
		t.Fatalf("tick after restart: %v", err)
	}
	if len(fired[id]) != 2 {
		t.Fatalf("escalations must survive restart, fired %v", fired)
	}
}

// TestReconcileRepairsCrashWindow covers the documented cross-instance gap:
// an expense that reached approved but never made it into the payment net
// (crash between the two transactions) is enqueued by Reconcile.
func TestReconcileRepairsCrashWindow(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "erin", 900)
	if _, err := app.Approve(ctx, id, "legal", "l"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Approve(ctx, id, "finance", "f"); err != nil {
		t.Fatal(err)
	}
	// Deliberately skip EnqueueApproved — this is the crash window.
	pay, err := app.Payment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pay.Payable) != 0 {
		t.Fatalf("precondition: payment net should be empty, got %d tokens", len(pay.Payable))
	}

	enqueued, marked, err := app.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if enqueued != 1 || marked != 0 {
		t.Fatalf("want 1 enqueued / 0 marked, got %d / %d", enqueued, marked)
	}
	pay, err = app.Payment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pay.Payable) != 1 || pay.PayableTotal != 900 {
		t.Fatalf("want the expense enqueued (total 900), got %d tokens total %.2f", len(pay.Payable), pay.PayableTotal)
	}

	// Reconcile is idempotent.
	enqueued, _, err = app.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if enqueued != 0 {
		t.Fatalf("second reconcile must be a no-op, enqueued %d", enqueued)
	}
}
