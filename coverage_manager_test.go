package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

func covManagerDef(t *testing.T) *workflow.Definition {
	t.Helper()
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func TestManagerEvictWorkflow(t *testing.T) {
	ctx := context.Background()
	reg := workflow.NewRegistry()
	store := NewMockStorage()
	mgr := workflow.NewManager(reg, store, workflow.WithRegistryCache())
	def := covManagerDef(t)

	wf, err := workflow.NewWorkflow("evict-1", def, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveWorkflow(ctx, "evict-1", wf); err != nil {
		t.Fatal(err)
	}

	// After eviction the cache no longer holds it; a fresh Load rebuilds from
	// storage. Eviction itself must not error or touch storage.
	mgr.EvictWorkflow("evict-1")
	if _, _, _, err := store.LoadState(ctx, "evict-1"); err != nil {
		t.Fatalf("state should survive eviction, got %v", err)
	}
	reloaded, err := mgr.LoadWorkflow(ctx, "evict-1", def)
	if err != nil {
		t.Fatalf("LoadWorkflow after evict = %v", err)
	}
	if reloaded == nil {
		t.Fatal("reloaded workflow is nil after eviction")
	}

	// Evicting an unknown ID is a harmless no-op.
	mgr.EvictWorkflow("does-not-exist")
}

func TestManagerUnsupportedCapabilities(t *testing.T) {
	ctx := context.Background()
	// MockStorage implements neither ListableStorage nor TokenQueryStorage.
	mgr := workflow.NewManager(workflow.NewRegistry(), NewMockStorage())

	_, err := mgr.ListWorkflowIDs(ctx, workflow.ListOptions{})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ListWorkflowIDs err = %v, want ErrUnsupported", err)
	}

	_, err = mgr.ListPlaceTokens(ctx, "a", workflow.ListOptions{})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ListPlaceTokens err = %v, want ErrUnsupported", err)
	}
}
