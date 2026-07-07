package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

// TestStrandedBranchEscalatesHarmlessly documents the cancellation-regions
// gap end to end: after legal rejects, finance's token is stranded in
// pending_finance and its 72h timer still fires. That must stay harmless —
// the expense reads rejected throughout, no actions reopen, and the timer
// goes quiet afterwards (escalated has no further timer).
func TestStrandedBranchEscalatesHarmlessly(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "alice", 150)

	if _, err := app.Reject(ctx, id, "legal", "lawyer"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	later := time.Now().Add(73 * time.Hour)
	fired, err := app.Tick(ctx, later)
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if len(fired[id]) != 1 || fired[id][0] != "finance_escalate" {
		t.Fatalf("want the stranded finance branch to escalate, fired %v", fired)
	}

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if view.State != "rejected" {
		t.Fatalf("state must stay rejected, got %q (marking %v)", view.State, view.Places)
	}
	if _, err := app.Approve(ctx, id, "finance", "cfo"); !errors.Is(err, ErrTerminal) {
		t.Fatalf("the stranded branch must not be approvable, got %v", err)
	}

	// The timer must now be quiet: escalated_finance has no timed
	// transition, so the instance drops out of the due index.
	fired, err = app.Tick(ctx, later.Add(100*time.Hour))
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("no further timers may fire on a closed expense, got %v", fired)
	}
}

// TestReconcileDeletesStaleDrafts covers the creation crash window: a crash
// between CreateWorkflow and the submit Execute leaves a context-less draft.
// Reconcile deletes it once it is older than the grace period — and not
// before.
func TestReconcileDeletesStaleDrafts(t *testing.T) {
	app, db := newTestApp(t)
	ctx := context.Background()

	// Simulate the crash: create the instance and never fire submit.
	if _, err := app.mgr.CreateWorkflow(ctx, "exp-crashed", app.expenseDef, "draft"); err != nil {
		t.Fatal(err)
	}

	// Within the grace period the draft is protected (it could be a
	// creation in flight).
	rep, err := app.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DraftsDeleted != 0 {
		t.Fatalf("a fresh draft must not be deleted, got %d", rep.DraftsDeleted)
	}

	// An app whose clock is past the grace period treats it as an artifact.
	future, err := NewApp(ctx, db, "sqlite3", 0, func() time.Time { return time.Now().Add(10 * time.Minute) })
	if err != nil {
		t.Fatal(err)
	}
	rep, err = future.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.DraftsDeleted != 1 {
		t.Fatalf("want the stale draft deleted, got %d", rep.DraftsDeleted)
	}
	if _, err := app.Expense(ctx, "exp-crashed"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("draft must be gone, got %v", err)
	}
}

// TestTickVsApproveRace races the escalation tick against both approvals.
// Both sides retry optimistic-concurrency conflicts internally; whatever
// interleaving wins, the expense must end approved with a coherent trail.
func TestTickVsApproveRace(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "bob", 500)
	later := time.Now().Add(73 * time.Hour)

	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 3 {
			if _, err := app.Tick(ctx, later); err != nil {
				errCh <- err
			}
		}
	}()
	for _, branch := range []string{"legal", "finance"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := app.Approve(ctx, id, branch, branch+"-bot"); err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("race produced an error: %v", err)
	}

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("approved") {
		t.Fatalf("want approved whatever the interleaving, marking %v", view.Places)
	}
}

// TestConcurrentBatchRuns: two simultaneous batch runs must pay each token
// exactly once (the loser's conflict retry sees an empty payable).
func TestConcurrentBatchRuns(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "carol", 700)
	for _, branch := range []string{"legal", "finance"} {
		if _, err := app.Approve(ctx, id, branch, branch); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.EnqueueApproved(ctx, id); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make([]*BatchResult, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = app.RunBatch(ctx, "op")
		}()
	}
	wg.Wait()
	totalPaid := 0
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("batch %d: %v", i, errs[i])
		}
		totalPaid += len(results[i].Paid)
	}
	if totalPaid != 1 {
		t.Fatalf("the token must be paid exactly once across both runs, got %d", totalPaid)
	}
	pay, err := app.Payment(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pay.PaidOut) != 1 || len(pay.Payable) != 0 {
		t.Fatalf("want 1 paid_out / 0 payable, got %d / %d", len(pay.PaidOut), len(pay.Payable))
	}
	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("paid") {
		t.Fatalf("expense must be marked paid, marking %v", view.Places)
	}
}

// TestSubmitValidation: garbage amounts and oversized fields are rejected
// before they can reach storage (ParseFloat happily produces Inf and NaN).
func TestSubmitValidation(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	cases := []struct {
		name      string
		submitter string
		amount    float64
	}{
		{"inf", "alice", math.Inf(1)},
		{"nan", "alice", math.NaN()},
		{"zero", "alice", 0},
		{"negative", "alice", -5},
		{"absurd", "alice", 1e13},
		{"no submitter", "  ", 100},
		{"long submitter", strings.Repeat("x", 121), 100},
	}
	for _, tc := range cases {
		if _, err := app.SubmitExpense(ctx, tc.submitter, "d", tc.amount); err == nil {
			t.Errorf("%s: want a validation error", tc.name)
		}
	}
	views, err := app.ListExpenses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("nothing may persist from rejected submissions, got %d", len(views))
	}
}

// TestSubmitValidationHTTP: the same garbage through the form endpoint is a
// 400, including textual Inf which ParseFloat accepts.
func TestSubmitValidationHTTP(t *testing.T) {
	ts, _ := newTestServer(t)
	for _, amount := range []string{"Inf", "NaN", "-3", "0", "abc", ""} {
		code, _ := postForm(t, ts.URL+"/expenses", url.Values{
			"submitter": {"alice"}, "amount": {amount},
		})
		if code != http.StatusBadRequest {
			t.Errorf("amount %q: want 400, got %d", amount, code)
		}
	}
}

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "ok") {
		t.Fatalf("healthz: %d %q", resp.StatusCode, body)
	}
}

// TestPostgresLifecycle runs the core lifecycle against PostgreSQL when
// EXPENSE_POSTGRES_DSN is set (same opt-in pattern as the storage
// conformance suite; skipped otherwise). It DROPS the app's tables in that
// database first — point it at a scratch database.
func TestPostgresLifecycle(t *testing.T) {
	dsn := os.Getenv("EXPENSE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("EXPENSE_POSTGRES_DSN not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, table := range []string{"workflow_states", "transition_history"} {
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+table+" CASCADE"); err != nil {
			t.Fatal(err)
		}
	}

	app, err := NewApp(ctx, db, "pgx", 0, time.Now)
	if err != nil {
		t.Fatalf("new app on postgres: %v", err)
	}
	id, err := app.SubmitExpense(ctx, "alice", "postgres run", 320)
	if err != nil {
		t.Fatal(err)
	}
	for _, branch := range []string{"legal", "finance"} {
		if _, err := app.Approve(ctx, id, branch, branch); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.EnqueueApproved(ctx, id); err != nil {
		t.Fatal(err)
	}
	res, err := app.RunBatch(ctx, "op")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paid) != 1 {
		t.Fatalf("want 1 paid, got %+v", res)
	}
	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("paid") {
		t.Fatalf("want paid on postgres, marking %v", view.Places)
	}
}
