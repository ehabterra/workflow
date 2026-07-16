// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
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

	// The escalated branch is still approvable — the same OR-input
	// legal_approve serves both stages, consuming the escalated token.
	names, err = app.Approve(ctx, id, "legal", "lawyer")
	if err != nil {
		t.Fatalf("approve escalated: %v", err)
	}
	if names[0] != "legal_approve" {
		t.Fatalf("want legal_approve (OR-input), fired %v", names)
	}

	// Timer firings are recorded in the audit trail atomically with the
	// state commit (WithFireDueTxSideEffect), actor "timer", with the
	// from/to markings of each step — no longer a post-hoc write.
	recs, err := app.hist.ListHistory(ctx, id, history.QueryOptions{Actor: "timer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 timer history records, got %d", len(recs))
	}
	for _, r := range recs {
		if r.FromState == "" || r.ToState == "" {
			t.Fatalf("timer record must carry the step's from/to marking, got %+v", r)
		}
		if !strings.Contains(r.ToState, "escalated") {
			t.Fatalf("timer record ToState = %q, want an escalated place", r.ToState)
		}
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

	rep, err := app.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if rep.Enqueued != 1 || rep.Marked != 0 {
		t.Fatalf("want 1 enqueued / 0 marked, got %d / %d", rep.Enqueued, rep.Marked)
	}
	pay, err = app.Payment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pay.Payable) != 1 || pay.PayableTotal != 900 {
		t.Fatalf("want the expense enqueued (total 900), got %d tokens total %.2f", len(pay.Payable), pay.PayableTotal)
	}

	// Reconcile is idempotent.
	rep, err = app.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Enqueued != 0 {
		t.Fatalf("second reconcile must be a no-op, enqueued %d", rep.Enqueued)
	}
}

// TestHistoryRecordsPerStepMarkings: a compound action (approve + finalize)
// must record each transition with the marking around THAT step, not one
// aggregate pair stamped on every row.
func TestHistoryRecordsPerStepMarkings(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "alice", 500)
	if _, err := app.Approve(ctx, id, "legal", "l"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Approve(ctx, id, "finance", "f"); err != nil {
		t.Fatal(err)
	}

	recs, err := app.hist.ListHistory(ctx, id, history.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]history.TransitionRecord{}
	for _, r := range recs {
		byName[r.Transition] = r
	}

	fa, ok := byName["finance_approve"]
	if !ok {
		t.Fatalf("missing finance_approve record: %+v", recs)
	}
	fin, ok := byName["finalize"]
	if !ok {
		t.Fatalf("missing finalize record: %+v", recs)
	}
	// finance_approve moved pending_finance -> finance_ok; finalize joined
	// legal_ok+finance_ok -> approved. Same fire() call, different snapshots.
	if !strings.Contains(fa.FromState, "pending_finance") || !strings.Contains(fa.ToState, "finance_ok") {
		t.Fatalf("finance_approve row has wrong marking: %s -> %s", fa.FromState, fa.ToState)
	}
	if strings.Contains(fin.FromState, "pending_finance") || !strings.Contains(fin.ToState, "approved") {
		t.Fatalf("finalize row has wrong marking: %s -> %s", fin.FromState, fin.ToState)
	}
	if fa.FromState == fin.FromState && fa.ToState == fin.ToState {
		t.Fatal("compound steps must not share one aggregate from/to pair")
	}
}
