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
	"fmt"
	"reflect"
	"testing"
	"time"

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

	// Due-index conformance, only if the backend supports it.
	if _, ok := newStore(t).(workflow.DueStorage); ok {
		runDue(t, newStore)
	}
}

// runDue exercises the workflow.DueStorage contract: the maintained next-due
// index and the ListDue fleet scan behind Manager.FireDue.
func runDue(t *testing.T, newStore Factory) {
	t.Helper()

	dueStore := func(t *testing.T) workflow.DueStorage {
		ds, ok := newStore(t).(workflow.DueStorage)
		if !ok {
			t.Fatal("store does not implement DueStorage")
		}
		return ds
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	saveDue := func(t *testing.T, ds workflow.DueStorage, id string, due *time.Time) int64 {
		t.Helper()
		v, err := ds.SaveVersionedStateWithDue(context.Background(), id, mk("s"), nil, 0, due)
		if err != nil {
			t.Fatalf("SaveVersionedStateWithDue(%s): %v", id, err)
		}
		return v
	}

	t.Run("Due/ListDueOrdersFiltersAndPages", func(t *testing.T) {
		ctx := context.Background()
		ds := dueStore(t)

		// IDs deliberately do NOT match due order (z<d1, m&b<d2, a<d3), so an
		// implementation that sorted by ID alone would fail: ordering must be by
		// due time first, with ID only as the tie-breaker.
		d1, d2, d3 := base.Add(1*time.Hour), base.Add(2*time.Hour), base.Add(3*time.Hour)
		saveDue(t, ds, "z", &d1)
		saveDue(t, ds, "m", &d2)
		saveDue(t, ds, "b", &d2)
		saveDue(t, ds, "a", &d3)
		saveDue(t, ds, "notimer", nil) // no running timer → never listed

		// before = d2 includes z (d1), then b and m (both d2, ID tie-break).
		if got, err := ds.ListDue(ctx, d2, 0); err != nil {
			t.Fatalf("ListDue(d2): %v", err)
		} else if want := []string{"z", "b", "m"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue(d2) = %v, want %v", got, want)
		}

		// Far in the future lists every timer-bearing instance, ordered by due
		// then ID, and never the nil-due one.
		if got, err := ds.ListDue(ctx, base.Add(1000*time.Hour), 0); err != nil {
			t.Fatalf("ListDue(future): %v", err)
		} else if want := []string{"z", "b", "m", "a"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue(future) = %v, want %v", got, want)
		}

		// A limit pages the ascending order.
		if got, err := ds.ListDue(ctx, base.Add(1000*time.Hour), 2); err != nil {
			t.Fatalf("ListDue(limit 2): %v", err)
		} else if want := []string{"z", "b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue(limit 2) = %v, want %v", got, want)
		}
	})

	t.Run("Due/BoundaryIsInclusive", func(t *testing.T) {
		ctx := context.Background()
		ds := dueStore(t)
		d := base.Add(time.Hour)
		saveDue(t, ds, "wf", &d)

		if got, err := ds.ListDue(ctx, d.Add(-time.Nanosecond), 0); err != nil {
			t.Fatalf("ListDue(just before): %v", err)
		} else if len(got) != 0 {
			t.Fatalf("ListDue(just before deadline) = %v, want none", got)
		}
		if got, err := ds.ListDue(ctx, d, 0); err != nil {
			t.Fatalf("ListDue(at): %v", err)
		} else if want := []string{"wf"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue(at deadline) = %v, want %v", got, want)
		}
	})

	t.Run("Due/ClearingDueRemovesFromIndex", func(t *testing.T) {
		ctx := context.Background()
		ds := dueStore(t)
		d := base.Add(time.Hour)
		v := saveDue(t, ds, "wf", &d)

		// Re-save with a nil due (as FireDue does when a timer stops running):
		// the instance must drop out of the index.
		if _, err := ds.SaveVersionedStateWithDue(ctx, "wf", mk("done"), nil, v, nil); err != nil {
			t.Fatalf("clear due: %v", err)
		}
		if got, err := ds.ListDue(ctx, base.Add(1000*time.Hour), 0); err != nil {
			t.Fatalf("ListDue after clear: %v", err)
		} else if len(got) != 0 {
			t.Fatalf("ListDue after clearing due = %v, want none", got)
		}
	})

	t.Run("Due/PlainVersionedSavePreservesDue", func(t *testing.T) {
		ctx := context.Background()
		ds := dueStore(t)
		d := base.Add(time.Hour)
		v := saveDue(t, ds, "wf", &d)

		// A plain (non-due) versioned save must leave the due column untouched, so
		// the instance still appears in the index.
		if _, err := ds.SaveVersionedState(ctx, "wf", mk("s2"), nil, v); err != nil {
			t.Fatalf("plain save: %v", err)
		}
		if got, err := ds.ListDue(ctx, base.Add(1000*time.Hour), 0); err != nil {
			t.Fatalf("ListDue after plain save: %v", err)
		} else if want := []string{"wf"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue after plain save = %v, want %v (due preserved)", got, want)
		}
	})

	// Transactional due-index conformance, only if the backend supports it.
	if _, ok := newStore(t).(workflow.TransactionalDueStorage); ok {
		runTxDue(t, newStore, base)
	}
}

// runTxDue exercises workflow.TransactionalDueStorage: SaveVersionedStateInTxWithDue
// must commit the state change, the due-index update, and every side effect as one
// atom — or roll all of them back together.
func runTxDue(t *testing.T, newStore Factory, base time.Time) {
	t.Helper()

	txDueStore := func(t *testing.T) workflow.TransactionalDueStorage {
		tds, ok := newStore(t).(workflow.TransactionalDueStorage)
		if !ok {
			t.Fatal("store does not implement TransactionalDueStorage")
		}
		return tds
	}
	far := base.Add(1000 * time.Hour)

	t.Run("Due/InTx/CommitsDueWithEffect", func(t *testing.T) {
		ctx := context.Background()
		tds := txDueStore(t)
		d := base.Add(time.Hour)
		effectRan := false
		v, err := tds.SaveVersionedStateInTxWithDue(ctx, "wf", mk("s"), nil, 0, &d,
			func(ctx context.Context, tx any) error { effectRan = true; return nil })
		if err != nil {
			t.Fatalf("SaveVersionedStateInTxWithDue: %v", err)
		}
		if v != 1 {
			t.Fatalf("version = %d, want 1", v)
		}
		if !effectRan {
			t.Fatal("side effect did not run")
		}
		// State, due, and effect all committed atomically: the instance is indexed.
		if got, err := tds.ListDue(ctx, far, 0); err != nil {
			t.Fatalf("ListDue: %v", err)
		} else if want := []string{"wf"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue = %v, want %v (due committed with effect)", got, want)
		}
	})

	t.Run("Due/InTx/NilDueRemovesFromIndex", func(t *testing.T) {
		ctx := context.Background()
		tds := txDueStore(t)
		d := base.Add(time.Hour)
		v, err := tds.SaveVersionedStateInTxWithDue(ctx, "wf", mk("s"), nil, 0, &d)
		if err != nil {
			t.Fatalf("initial in-tx save: %v", err)
		}
		// A nil due (timer stopped) clears the index inside the transaction.
		if _, err := tds.SaveVersionedStateInTxWithDue(ctx, "wf", mk("done"), nil, v, nil); err != nil {
			t.Fatalf("clear due in tx: %v", err)
		}
		if got, err := tds.ListDue(ctx, far, 0); err != nil {
			t.Fatalf("ListDue: %v", err)
		} else if len(got) != 0 {
			t.Fatalf("ListDue after clearing due = %v, want none", got)
		}
	})

	t.Run("Due/InTx/VersionConflictLeavesDueUntouched", func(t *testing.T) {
		ctx := context.Background()
		tds := txDueStore(t)
		d := base.Add(time.Hour)
		if _, err := tds.SaveVersionedStateWithDue(ctx, "wf", mk("s"), nil, 0, &d); err != nil {
			t.Fatalf("seed: %v", err)
		}
		// A stale expected version conflicts; the proposed new due must not apply.
		newDue := base.Add(500 * time.Hour)
		if _, err := tds.SaveVersionedStateInTxWithDue(ctx, "wf", mk("x"), nil, 99, &newDue); !errors.Is(err, workflow.ErrConflict) {
			t.Fatalf("stale in-tx save err = %v, want ErrConflict", err)
		}
		// The original due is intact: listed at d, not shifted out to newDue.
		if got, err := tds.ListDue(ctx, d, 0); err != nil {
			t.Fatalf("ListDue(d): %v", err)
		} else if want := []string{"wf"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ListDue(d) = %v, want %v (original due untouched)", got, want)
		}
	})

	t.Run("Due/InTx/EffectErrorRollsBackDueWrite", func(t *testing.T) {
		ctx := context.Background()
		tds := txDueStore(t)
		d := base.Add(time.Hour)
		boom := errors.New("effect boom")
		if _, err := tds.SaveVersionedStateInTxWithDue(ctx, "wf", mk("s"), nil, 0, &d,
			func(ctx context.Context, tx any) error { return boom }); err == nil {
			t.Fatal("SaveVersionedStateInTxWithDue with failing effect: want error, got nil")
		}
		// The whole transaction rolled back: no row, and nothing in the index.
		if _, _, _, err := tds.LoadVersionedState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("LoadVersionedState after rollback err = %v, want ErrWorkflowNotFound", err)
		}
		if got, err := tds.ListDue(ctx, far, 0); err != nil {
			t.Fatalf("ListDue: %v", err)
		} else if len(got) != 0 {
			t.Fatalf("ListDue after rolled-back effect = %v, want none (due write rolled back)", got)
		}
	})
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

	// Regression for a lost-update bug found by the M5 dogfood: reading the
	// marking and the version in two separate queries let a concurrent commit
	// slip in between, pairing a stale marking with the new version — and a
	// later save from that snapshot passed the version check and overwrote
	// the concurrent update. The invariant here makes any skew visible: the
	// row at version V always holds exactly the marking "p<V>", maintained by
	// a single writer, so every concurrent load must observe a matching pair.
	t.Run("Versioned/LoadIsAtomicSnapshot", func(t *testing.T) {
		ctx := context.Background()
		vs := versioned(t)
		if _, err := vs.SaveVersionedState(ctx, "wf", mk("p1"), nil, 0); err != nil {
			t.Fatalf("create: %v", err)
		}

		const writes = 300
		done := make(chan struct{})
		writeErr := make(chan error, 1)
		go func() {
			defer close(done)
			v := int64(1)
			for range writes {
				next := v + 1
				nv, err := vs.SaveVersionedState(ctx, "wf", mk(workflow.Place(fmt.Sprintf("p%d", next))), nil, v)
				if err != nil {
					writeErr <- err
					return
				}
				v = nv
			}
		}()

		for {
			select {
			case <-done:
				if len(writeErr) > 0 {
					t.Fatalf("writer: %v", <-writeErr)
				}
				return
			default:
			}
			m, _, version, err := vs.LoadVersionedState(ctx, "wf")
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			places := m.Places()
			if len(places) != 1 || string(places[0]) != fmt.Sprintf("p%d", version) {
				t.Fatalf("read skew: marking %v paired with version %d (want [p%d])", places, version, version)
			}
		}
	})
}
