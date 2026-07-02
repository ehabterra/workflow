// Package storagetest provides a reusable conformance suite for workflow.Storage
// implementations. Any backend (SQLite, Postgres, an in-memory mock, …) can run
// Run against a factory to verify it honors the Storage contract, and — when the
// backend also implements workflow.VersionedStorage — the optimistic-concurrency
// contract as well.
//
// Usage from a backend's tests:
//
//	func TestConformance(t *testing.T) {
//	    storagetest.Run(t, func(t *testing.T) workflow.Storage {
//	        db := openFreshDB(t)
//	        store, _ := storage.NewSQLiteStorage(db)
//	        _ = storage.Initialize(db, store.GenerateSchema())
//	        return store
//	    })
//	}
package storagetest

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

// Factory returns a fresh, initialized Storage for a single subtest. It should
// register any cleanup via t.Cleanup. It is called once per subtest so each runs
// against isolated state.
type Factory func(t *testing.T) workflow.Storage

// Run executes the full Storage conformance suite against newStore. If the store
// returned by newStore also implements workflow.VersionedStorage, the versioning
// conformance suite is run too.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("SaveLoadRoundTrip", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		if err := store.SaveState(ctx, "wf", []workflow.Place{"review"}, nil); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		places, _, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if len(places) != 1 || places[0] != "review" {
			t.Fatalf("places = %v, want [review]", places)
		}
	})

	t.Run("LoadMissingReturnsNotFound", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		if _, _, err := store.LoadState(ctx, "nope"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("LoadState(missing) err = %v, want ErrWorkflowNotFound", err)
		}
	})

	t.Run("Overwrite", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		if err := store.SaveState(ctx, "wf", []workflow.Place{"a"}, nil); err != nil {
			t.Fatalf("first SaveState: %v", err)
		}
		if err := store.SaveState(ctx, "wf", []workflow.Place{"b"}, nil); err != nil {
			t.Fatalf("second SaveState: %v", err)
		}
		places, _, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if len(places) != 1 || places[0] != "b" {
			t.Fatalf("places = %v, want [b] (overwrite)", places)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		if err := store.SaveState(ctx, "wf", []workflow.Place{"a"}, nil); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		if err := store.DeleteState(ctx, "wf"); err != nil {
			t.Fatalf("DeleteState: %v", err)
		}
		if _, _, err := store.LoadState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("LoadState after delete err = %v, want ErrWorkflowNotFound", err)
		}
	})

	t.Run("MultiplePlaces", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		want := []workflow.Place{"qa", "security"}
		if err := store.SaveState(ctx, "wf", want, nil); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		places, _, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if len(places) != len(want) {
			t.Fatalf("places = %v, want %v", places, want)
		}
	})

	// Versioning conformance, only if the backend supports it.
	if _, ok := newStore(t).(workflow.VersionedStorage); ok {
		runVersioned(t, newStore)
	}
}

func runVersioned(t *testing.T, newStore Factory) {
	t.Helper()

	versioned := func(t *testing.T) workflow.VersionedStorage {
		vs, ok := newStore(t).(workflow.VersionedStorage)
		if !ok {
			t.Fatal("store does not implement VersionedStorage")
		}
		return vs
	}

	t.Run("Versioned/CreateStartsAtOne", func(t *testing.T) {
		ctx := context.Background()
		vs := versioned(t)
		v, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"a"}, nil, 0)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if v != 1 {
			t.Fatalf("version = %d, want 1", v)
		}
	})

	t.Run("Versioned/LoadReturnsVersion", func(t *testing.T) {
		ctx := context.Background()
		vs := versioned(t)
		if _, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"a"}, nil, 0); err != nil {
			t.Fatalf("create: %v", err)
		}
		_, _, v, err := vs.LoadVersionedState(ctx, "wf")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if v != 1 {
			t.Fatalf("loaded version = %d, want 1", v)
		}
	})

	t.Run("Versioned/CreateExistingConflicts", func(t *testing.T) {
		ctx := context.Background()
		vs := versioned(t)
		if _, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"a"}, nil, 0); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"x"}, nil, 0); !errors.Is(err, workflow.ErrConflict) {
			t.Fatalf("recreate err = %v, want ErrConflict", err)
		}
	})

	t.Run("Versioned/StaleUpdateConflicts", func(t *testing.T) {
		ctx := context.Background()
		vs := versioned(t)
		if _, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"a"}, nil, 0); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"b"}, nil, 1); err != nil {
			t.Fatalf("update: %v", err)
		}
		if _, err := vs.SaveVersionedState(ctx, "wf", []workflow.Place{"c"}, nil, 1); !errors.Is(err, workflow.ErrConflict) {
			t.Fatalf("stale update err = %v, want ErrConflict", err)
		}
	})
}
