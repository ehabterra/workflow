package main

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow/storage"
	"github.com/ehabterra/workflow/workflowtest"
)

// TestPaymentAnchorMigration proves the place-removal upgrade path: a payment
// instance persisted by a pre-upgrade binary still holds the always-marked
// batch_control anchor (plus its real expense tokens). Loading it through the
// app's migration hook must strip the anchor from the STORED marking — place
// removal is the one definition change approve-and-revalidate alone cannot
// cover — while the tokens survive untouched.
func TestPaymentAnchorMigration(t *testing.T) {
	app, db := newTestApp(t)
	ctx := context.Background()

	// A petty-cash expense auto-approves and lands as a token in payable.
	id := mustSubmit(t, app, "alice", 42)

	// Regress the stored payment state to the pre-upgrade shape: same
	// tokens, plus the anchor, minus the definition fingerprint (a
	// pre-upgrade binary stamped the old definition's hash; an absent one
	// exercises the same mismatch path).
	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	marking, _, version, err := store.LoadState(ctx, paymentID)
	if err != nil {
		t.Fatalf("load payment state: %v", err)
	}
	if got := len(marking.TokensAt("payable")); got != 1 {
		t.Fatalf("precondition: expected 1 payable token, got %d", got)
	}
	if err := marking.AddPlace("batch_control"); err != nil {
		t.Fatalf("re-adding the anchor: %v", err)
	}
	if _, err := store.SaveState(ctx, paymentID, marking, map[string]any{}, version); err != nil {
		t.Fatalf("regress payment state: %v", err)
	}

	// Loading through the app fires the hook and migrates: anchor gone,
	// token intact.
	wf, err := app.mgr.LoadWorkflow(ctx, paymentID, app.paymentDef)
	if err != nil {
		t.Fatalf("load after regression: %v", err)
	}
	workflowtest.AssertNotHas(t, wf, "batch_control")
	if got := len(wf.Marking().TokensAt("payable")); got != 1 {
		t.Fatalf("payable token lost in migration: %+v", wf.Marking().AllTokens())
	}

	// The rewrite landed in STORAGE, not just in the loaded instance, and a
	// second load (the hook fires again until a save restamps the
	// fingerprint) is a clean no-op.
	stored, _, _, err := store.LoadState(ctx, paymentID)
	if err != nil {
		t.Fatalf("reload raw state: %v", err)
	}
	if stored.HasPlace("batch_control") {
		t.Fatal("stored marking still holds the anchor")
	}
	if _, err := app.mgr.LoadWorkflow(ctx, paymentID, app.paymentDef); err != nil {
		t.Fatalf("second load after migration: %v", err)
	}

	// The migrated pool still works end-to-end: the batch pays the expense
	// out and payable is empty again (paid_out keeps the token as the
	// cross-instance audit/reconcile record).
	res, err := app.RunBatch(ctx, "system")
	if err != nil {
		t.Fatalf("run batch after migration: %v", err)
	}
	if len(res.Paid) != 1 || res.Paid[0] != id || res.Held != 0 {
		t.Fatalf("batch after migration: paid %v held %d, want [%s] paid 0 held", res.Paid, res.Held, id)
	}
	wf, err = app.mgr.LoadWorkflow(ctx, paymentID, app.paymentDef)
	if err != nil {
		t.Fatalf("load after batch: %v", err)
	}
	workflowtest.AssertNotHas(t, wf, "payable", "batch_control")

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatalf("expense view: %v", err)
	}
	if !view.Has("paid") {
		t.Fatalf("expense should be paid after the batch, marking %v", view.Places)
	}
}
