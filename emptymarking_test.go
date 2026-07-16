// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

// poolDefinition builds the minimal token-pool net: payable --pay--> paid_out,
// no place that is always marked. Between batches every place is legitimately
// empty, which is exactly the shape that used to require an anchor place.
func poolDefinition(t *testing.T) *workflow.Definition {
	t.Helper()
	pay, err := workflow.NewTransition("pay", []workflow.Place{"payable"}, []workflow.Place{"paid_out"})
	if err != nil {
		t.Fatalf("NewTransition: %v", err)
	}
	def, err := workflow.NewDefinition([]workflow.Place{"payable", "paid_out"}, []workflow.Transition{*pay})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

func TestNewWorkflowFromMarking_EmptyMarkingIsValid(t *testing.T) {
	def := poolDefinition(t)

	wf, err := workflow.NewWorkflowFromMarking("pool", def, workflow.NewMarking(nil))
	if err != nil {
		t.Fatalf("empty marking must be a valid start for a token-pool net, got: %v", err)
	}
	if places := wf.Marking().Places(); len(places) != 0 {
		t.Fatalf("expected no marked places, got %v", places)
	}
	if places := wf.InitialPlaces(); len(places) != 0 {
		t.Fatalf("expected no initial places, got %v", places)
	}

	// Nothing is enabled until a token arrives; then the net fires normally.
	if err := wf.ApplyTransition("pay"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("expected ErrNotEnabled on the empty pool, got: %v", err)
	}
	if _, err := wf.CreateToken("payable", workflow.TokenData{"amount": 42.0}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := wf.ApplyTransition("pay"); err != nil {
		t.Fatalf("pay with a token present: %v", err)
	}

	// A nil marking is still rejected — only an EMPTY one is meaningful.
	if _, err := workflow.NewWorkflowFromMarking("pool2", def, nil); err == nil {
		t.Fatal("nil marking must still be rejected")
	}
}

// TestManager_EmptyMarkingRoundTrip is the FRICTION #3 regression test: a pool
// net whose places are all empty must create, save, load, receive tokens,
// drain back to empty, and reload — with no always-marked anchor place.
func TestManager_EmptyMarkingRoundTrip(t *testing.T) {
	ctx := context.Background()
	def := poolDefinition(t)
	storage := NewMockStorage()
	manager := workflow.NewManager(workflow.NewRegistry(), storage)

	if _, err := manager.CreateWorkflowFromMarking(ctx, "pool", def, workflow.NewMarking(nil)); err != nil {
		t.Fatalf("CreateWorkflowFromMarking(empty): %v", err)
	}

	// Load the freshly created empty instance (used to fail with
	// "loaded state has no places").
	wf, err := manager.LoadWorkflow(ctx, "pool", def)
	if err != nil {
		t.Fatalf("loading an empty-marking instance: %v", err)
	}
	if places := wf.Marking().Places(); len(places) != 0 {
		t.Fatalf("expected empty marking after load, got %v", places)
	}

	// A token flows through the pool and drains it back to empty…
	if _, err := wf.CreateToken("payable", workflow.TokenData{"expense_id": "exp-1"}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := manager.SaveWorkflow(ctx, "pool", wf); err != nil {
		t.Fatalf("saving pool with one token: %v", err)
	}
	wf, err = manager.LoadWorkflow(ctx, "pool", def)
	if err != nil {
		t.Fatalf("reloading pool with one token: %v", err)
	}
	if got := len(wf.Marking().TokensAt("payable")); got != 1 {
		t.Fatalf("expected the colored token to round-trip, got %d tokens", got)
	}
	if err := wf.ApplyTransition("pay"); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if err := wf.Marking().RemovePlace("paid_out"); err != nil {
		t.Fatalf("draining paid_out: %v", err)
	}
	if err := manager.SaveWorkflow(ctx, "pool", wf); err != nil {
		t.Fatalf("saving the drained (empty again) pool: %v", err)
	}

	// …and the empty state survives another load.
	wf, err = manager.LoadWorkflow(ctx, "pool", def)
	if err != nil {
		t.Fatalf("reloading the drained pool: %v", err)
	}
	if places := wf.Marking().Places(); len(places) != 0 {
		t.Fatalf("expected the drained pool to reload empty, got %v", places)
	}
}

func TestManager_CreateWorkflowFromMarking_MultiPlace(t *testing.T) {
	ctx := context.Background()
	def := poolDefinition(t)
	storage := NewMockStorage()
	manager := workflow.NewManager(workflow.NewRegistry(), storage)

	initial := workflow.NewMarking(nil)
	initial.AddToken("payable", workflow.NewToken(workflow.TokenData{"amount": 7.0}))
	if err := initial.AddPlace("paid_out"); err != nil {
		t.Fatalf("AddPlace: %v", err)
	}
	if _, err := manager.CreateWorkflowFromMarking(ctx, "seeded", def, initial); err != nil {
		t.Fatalf("CreateWorkflowFromMarking(seeded): %v", err)
	}

	wf, err := manager.LoadWorkflow(ctx, "seeded", def)
	if err != nil {
		t.Fatalf("loading seeded instance: %v", err)
	}
	if got := len(wf.Marking().TokensAt("payable")); got != 1 {
		t.Fatalf("expected 1 colored token in payable, got %d", got)
	}
	if !wf.Marking().HasPlace("paid_out") {
		t.Fatal("expected paid_out to be marked")
	}

	// An undefined place is still rejected.
	bad := workflow.NewMarking([]workflow.Place{"nope"})
	if _, err := manager.CreateWorkflowFromMarking(ctx, "bad", def, bad); err == nil {
		t.Fatal("expected an error for a marking with an undefined place")
	}
}
