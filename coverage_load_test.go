package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

func twoPlaceDef(t *testing.T, extra ...workflow.Place) *workflow.Definition {
	t.Helper()
	places := append([]workflow.Place{"pending", "approved"}, extra...)
	def, err := workflow.NewDefinition(
		places,
		[]workflow.Transition{*workflow.MustNewTransition("approve", []workflow.Place{"pending"}, []workflow.Place{"approved"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestLoadWorkflowMissingID(t *testing.T) {
	mgr := workflow.NewManager(workflow.NewRegistry(), NewMockStorage())
	_, err := mgr.LoadWorkflow(context.Background(), "ghost", twoPlaceDef(t))
	if err == nil {
		t.Fatal("loading a non-existent workflow should error")
	}
}

func TestLoadWorkflowStalePlaceRejected(t *testing.T) {
	ctx := context.Background()
	store := NewMockStorage()
	mgr := workflow.NewManager(workflow.NewRegistry(), store)
	def := twoPlaceDef(t)

	wf, err := workflow.NewWorkflow("inst", def, "pending")
	if err != nil {
		t.Fatal(err)
	}
	// A real save stamps the definition fingerprint into the stored context.
	if err := mgr.SaveWorkflow(ctx, "inst", wf); err != nil {
		t.Fatal(err)
	}
	// Now corrupt only the persisted marking so it references a place the
	// definition does not have — fingerprint still matches, so this exercises
	// the per-place validation loop rather than the fingerprint check.
	store.states["inst"] = []byte(`["ghost"]`)

	_, err = mgr.LoadWorkflow(ctx, "inst", def)
	if !errors.Is(err, workflow.ErrDefinitionMismatch) {
		t.Fatalf("stale-place load = %v, want ErrDefinitionMismatch", err)
	}
}

func TestCheckCachedDefinitionMismatch(t *testing.T) {
	ctx := context.Background()
	store := NewMockStorage()
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithRegistryCache())

	def := twoPlaceDef(t)
	wf, err := workflow.NewWorkflow("cached", def, "pending")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveWorkflow(ctx, "cached", wf); err != nil {
		t.Fatal(err)
	}
	// Prime the cache.
	if _, err := mgr.LoadWorkflow(ctx, "cached", def); err != nil {
		t.Fatal(err)
	}

	// A structurally different definition (extra place => different fingerprint)
	// must be rejected against the cached instance.
	other := twoPlaceDef(t, "extra")
	if _, err := mgr.LoadWorkflow(ctx, "cached", other); !errors.Is(err, workflow.ErrDefinitionMismatch) {
		t.Fatalf("cache load with foreign definition = %v, want ErrDefinitionMismatch", err)
	}

	// A different *pointer* with the SAME fingerprint is accepted (fast path miss,
	// fingerprint slow path hit).
	same := twoPlaceDef(t)
	if _, err := mgr.LoadWorkflow(ctx, "cached", same); err != nil {
		t.Fatalf("cache load with equivalent definition = %v, want nil", err)
	}
}
