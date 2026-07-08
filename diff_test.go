package workflow_test

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow"
)

// diffNet builds a small definition from a place list and (name, from, to,
// guard) transition tuples, for exercising the structural diff.
func diffNet(t *testing.T, places []workflow.Place, trans [][4]string) *workflow.Definition {
	t.Helper()
	var ts []workflow.Transition
	for _, tr := range trans {
		tt := workflow.MustNewTransition(tr[0], []workflow.Place{workflow.Place(tr[1])}, []workflow.Place{workflow.Place(tr[2])})
		if tr[3] != "" {
			tt.SetMetadata("guard", tr[3])
		}
		ts = append(ts, *tt)
	}
	def, err := workflow.NewDefinition(places, ts)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// captureMismatch returns a Manager whose migration handler approves every
// mismatch and records it for assertions.
func captureMismatch(store workflow.Storage, out *workflow.DefinitionMismatch) *workflow.Manager {
	return workflow.NewManager(workflow.NewRegistry(), store,
		workflow.WithDefinitionMigration(func(_ context.Context, mm workflow.DefinitionMismatch) error {
			*out = mm
			return nil
		}))
}

// TestDefinitionDiff_RemovalAndGuardChange: the diff distinguishes what the
// bare fingerprint never could — a removed transition and a rewired one, by
// name — so a hook can refuse non-additive changes specifically.
func TestDefinitionDiff_RemovalAndGuardChange(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()

	v1 := diffNet(t, []workflow.Place{"a", "b", "c"}, [][4]string{
		{"go", "a", "b", ""},
		{"extra", "b", "c", ""},
	})
	mgr := workflow.NewManager(workflow.NewRegistry(), store)
	if _, err := mgr.CreateWorkflow(ctx, "wf", v1, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	// v2 drops "extra" and puts a guard on "go".
	v2 := diffNet(t, []workflow.Place{"a", "b", "c"}, [][4]string{
		{"go", "a", "b", "amount > 10"},
	})

	var mm workflow.DefinitionMismatch
	if _, err := captureMismatch(store, &mm).LoadWorkflow(ctx, "wf", v2); err != nil {
		t.Fatalf("LoadWorkflow(v2): %v", err)
	}
	if mm.Diff == nil {
		t.Fatal("Diff = nil, want structural diff")
	}
	if got := mm.Diff.TransitionsRemoved; len(got) != 1 || got[0] != "extra" {
		t.Errorf("TransitionsRemoved = %v, want [extra]", got)
	}
	if got := mm.Diff.TransitionsChanged; len(got) != 1 || got[0] != "go" {
		t.Errorf("TransitionsChanged = %v, want [go] (guard change)", got)
	}
	if len(mm.Diff.PlacesAdded) != 0 || len(mm.Diff.PlacesRemoved) != 0 {
		t.Errorf("place diff = +%v -%v, want none", mm.Diff.PlacesAdded, mm.Diff.PlacesRemoved)
	}
	if mm.Diff.Additive() {
		t.Errorf("diff %q must not be additive", mm.Diff)
	}
}

// TestDefinitionDiff_RenameReadsAsRemoveAddChange: a renamed place appears as
// one removed + one added, with the referencing transition changed — the
// exact shape a reviewer needs to tell a rename from an addition.
func TestDefinitionDiff_RenameReadsAsRemoveAddChange(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()

	v1 := diffNet(t, []workflow.Place{"a", "b"}, [][4]string{{"go", "a", "b", ""}})
	mgr := workflow.NewManager(workflow.NewRegistry(), store)
	if _, err := mgr.CreateWorkflow(ctx, "wf", v1, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	v2 := diffNet(t, []workflow.Place{"a", "b2"}, [][4]string{{"go", "a", "b2", ""}})

	var mm workflow.DefinitionMismatch
	if _, err := captureMismatch(store, &mm).LoadWorkflow(ctx, "wf", v2); err != nil {
		t.Fatalf("LoadWorkflow(v2): %v", err)
	}
	if mm.Diff == nil {
		t.Fatal("Diff = nil, want structural diff")
	}
	d := mm.Diff
	if len(d.PlacesAdded) != 1 || d.PlacesAdded[0] != "b2" ||
		len(d.PlacesRemoved) != 1 || d.PlacesRemoved[0] != "b" {
		t.Errorf("place diff = +%v -%v, want +[b2] -[b]", d.PlacesAdded, d.PlacesRemoved)
	}
	if len(d.TransitionsChanged) != 1 || d.TransitionsChanged[0] != "go" {
		t.Errorf("TransitionsChanged = %v, want [go] (its output moved)", d.TransitionsChanged)
	}
	if got, want := d.String(), "+places[b2] -places[b] ~transitions[go]"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestDefinitionDiff_NilForPreShapeState: state saved without a shape (an
// older library version, or a row not written by this library) yields a nil
// Diff — "no information", while the fingerprints still flow through.
func TestDefinitionDiff_NilForPreShapeState(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()

	def := diffNet(t, []workflow.Place{"a", "b"}, [][4]string{{"go", "a", "b", ""}})

	// Simulate a pre-shape row: fingerprint stamped, no shape key.
	if _, err := store.SaveState(ctx, "old",
		workflow.NewMarking([]workflow.Place{"a"}),
		map[string]any{"__workflow_def_fingerprint": "deadbeef"}, 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var mm workflow.DefinitionMismatch
	if _, err := captureMismatch(store, &mm).LoadWorkflow(ctx, "old", def); err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if mm.StoredFingerprint != "deadbeef" || mm.CurrentFingerprint == "" {
		t.Errorf("fingerprints = %q -> %q, want deadbeef -> current", mm.StoredFingerprint, mm.CurrentFingerprint)
	}
	if mm.Diff != nil {
		t.Errorf("Diff = %v, want nil for a pre-shape row", mm.Diff)
	}
}
