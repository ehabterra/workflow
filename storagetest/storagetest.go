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
	"reflect"
	"testing"

	"github.com/ehabterra/workflow"
)

// Factory returns a fresh, initialized Storage for a single subtest. It should
// register any cleanup via t.Cleanup. It is called once per subtest so each runs
// against isolated state.
type Factory func(t *testing.T) workflow.Storage

// mk builds a boolean marking with a single (uncolored) token at each place.
func mk(places ...workflow.Place) workflow.Marking {
	return workflow.NewMarking(places)
}

// Run executes the full Storage conformance suite against newStore. If the store
// returned by newStore also implements workflow.VersionedStorage, the versioning
// conformance suite is run too.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	t.Run("SaveLoadRoundTrip", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		if err := store.SaveState(ctx, "wf", mk("review"), nil); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		m, _, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if places := m.Places(); len(places) != 1 || places[0] != "review" {
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
		if err := store.SaveState(ctx, "wf", mk("a"), nil); err != nil {
			t.Fatalf("first SaveState: %v", err)
		}
		if err := store.SaveState(ctx, "wf", mk("b"), nil); err != nil {
			t.Fatalf("second SaveState: %v", err)
		}
		m, _, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if places := m.Places(); len(places) != 1 || places[0] != "b" {
			t.Fatalf("places = %v, want [b] (overwrite)", places)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)
		if err := store.SaveState(ctx, "wf", mk("a"), nil); err != nil {
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
		if err := store.SaveState(ctx, "wf", mk("qa", "security"), nil); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		m, _, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if places := m.Places(); len(places) != 2 {
			t.Fatalf("places = %v, want 2 places", places)
		}
	})

	t.Run("ColoredTokensRoundTrip", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)

		m := workflow.NewMarking(nil)
		m.AddToken("pending", workflow.NewTokenWithID("t1", workflow.TokenData{"order": "A", "amount": float64(100)}))
		m.AddToken("pending", workflow.NewTokenWithID("t2", workflow.TokenData{"order": "B", "amount": float64(250)}))
		if err := store.SaveState(ctx, "batch", m, nil); err != nil {
			t.Fatalf("SaveState: %v", err)
		}

		loaded, _, err := store.LoadState(ctx, "batch")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		if got := loaded.TokenCount("pending"); got != 2 {
			t.Fatalf("TokenCount(pending) = %d, want 2", got)
		}
		if !loaded.HasToken("pending", "t1") || !loaded.HasToken("pending", "t2") {
			t.Fatalf("loaded marking lost token identities: %+v", loaded.AllTokens())
		}
		// Verify a token's data survived the round-trip.
		for _, tok := range loaded.TokensAt("pending") {
			if tok.ID() == "t2" {
				if v, _ := tok.Get("amount"); v != float64(250) {
					t.Fatalf("token t2 amount = %v, want 250", v)
				}
			}
		}
	})

	t.Run("FullContextRoundTrip", func(t *testing.T) {
		ctx := context.Background()
		store := newStore(t)

		// Arbitrary keys — none pre-declared as custom-field columns — must
		// survive the round-trip. JSON-encoded values may change type in the
		// usual encoding/json ways (numbers come back as float64).
		saved := map[string]any{
			"requester": "alice",
			"urgent":    true,
			"amount":    float64(1250.5),
			"tags":      []any{"travel", "q3"},
			"approver":  map[string]any{"dept": "finance", "level": float64(2)},
		}
		if err := store.SaveState(ctx, "wf", mk("submitted"), saved); err != nil {
			t.Fatalf("SaveState: %v", err)
		}
		_, got, err := store.LoadState(ctx, "wf")
		if err != nil {
			t.Fatalf("LoadState: %v", err)
		}
		for key, want := range saved {
			if !reflect.DeepEqual(got[key], want) {
				t.Errorf("context[%q] = %#v (%T), want %#v (%T)", key, got[key], got[key], want, want)
			}
		}
	})

	t.Run("ListIDs", func(t *testing.T) {
		store := newStore(t)
		ls, ok := store.(workflow.ListableStorage)
		if !ok {
			t.Skip("backend does not implement ListableStorage")
		}
		ctx := context.Background()

		if ids, err := ls.ListIDs(ctx, workflow.ListOptions{}); err != nil {
			t.Fatalf("ListIDs(empty): %v", err)
		} else if len(ids) != 0 {
			t.Fatalf("ListIDs on empty store = %v, want none", ids)
		}

		for _, id := range []string{"c", "a", "b"} {
			if err := store.SaveState(ctx, id, mk("s"), nil); err != nil {
				t.Fatalf("SaveState(%s): %v", id, err)
			}
		}

		all, err := ls.ListIDs(ctx, workflow.ListOptions{})
		if err != nil {
			t.Fatalf("ListIDs(all): %v", err)
		}
		if want := []string{"a", "b", "c"}; !reflect.DeepEqual(all, want) {
			t.Fatalf("ListIDs = %v, want %v (sorted)", all, want)
		}

		page, err := ls.ListIDs(ctx, workflow.ListOptions{Limit: 2})
		if err != nil {
			t.Fatalf("ListIDs(limit 2): %v", err)
		}
		if want := []string{"a", "b"}; !reflect.DeepEqual(page, want) {
			t.Fatalf("ListIDs(limit 2) = %v, want %v", page, want)
		}

		page2, err := ls.ListIDs(ctx, workflow.ListOptions{Limit: 2, Offset: 2})
		if err != nil {
			t.Fatalf("ListIDs(limit 2, offset 2): %v", err)
		}
		if want := []string{"c"}; !reflect.DeepEqual(page2, want) {
			t.Fatalf("ListIDs(offset 2) = %v, want %v", page2, want)
		}

		// Offset without a limit exercises the engine-specific "no limit"
		// clause (SQLite requires LIMIT -1 before OFFSET; Postgres does not).
		rest, err := ls.ListIDs(ctx, workflow.ListOptions{Offset: 1})
		if err != nil {
			t.Fatalf("ListIDs(offset only): %v", err)
		}
		if want := []string{"b", "c"}; !reflect.DeepEqual(rest, want) {
			t.Fatalf("ListIDs(offset only) = %v, want %v", rest, want)
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
		v, err := vs.SaveVersionedState(ctx, "wf", mk("a"), nil, 0)
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
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("a"), nil, 0); err != nil {
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
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("a"), nil, 0); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("x"), nil, 0); !errors.Is(err, workflow.ErrConflict) {
			t.Fatalf("recreate err = %v, want ErrConflict", err)
		}
	})

	t.Run("Versioned/StaleUpdateConflicts", func(t *testing.T) {
		ctx := context.Background()
		vs := versioned(t)
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("a"), nil, 0); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("b"), nil, 1); err != nil {
			t.Fatalf("update: %v", err)
		}
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("c"), nil, 1); !errors.Is(err, workflow.ErrConflict) {
			t.Fatalf("stale update err = %v, want ErrConflict", err)
		}
	})
}
