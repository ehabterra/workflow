package storage_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

var e2eEpoch = time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)

// escalationDef mirrors the canonical timer workflow used across the timer
// tests: submitted --approve--> approved and submitted --escalate(after)-->
// escalated.
func escalationDef(t *testing.T, after time.Duration) *workflow.Definition {
	t.Helper()
	approve := workflow.MustNewTransition("approve", []workflow.Place{"submitted"}, []workflow.Place{"approved"})
	escalate := workflow.MustNewTransition("escalate", []workflow.Place{"submitted"}, []workflow.Place{"escalated"})
	escalate.SetTimeoutAfter(after)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"submitted", "approved", "escalated"},
		[]workflow.Transition{*approve, *escalate},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// seedSubmitted persists a submitted instance whose token entered at t0, so its
// escalation deadline (t0+after) is deterministic and testable without sleeping.
func seedSubmitted(t *testing.T, mgr *workflow.Manager, id string, def *workflow.Definition, t0 time.Time) {
	t.Helper()
	wf, err := workflow.NewWorkflow(id, def, "submitted", workflow.WithClock(func() time.Time { return t0 }))
	if err != nil {
		t.Fatalf("NewWorkflow(%s): %v", id, err)
	}
	wf.SetManager(mgr)
	if err := mgr.SaveWorkflow(context.Background(), id, wf); err != nil {
		t.Fatalf("SaveWorkflow(%s): %v", id, err)
	}
}

// TestSQLiteDueMigration proves EnsureSchema safely adds the due column and its
// index to a pre-existing table created before M4 (simulated with an empty due
// column), and that due operations then work — old rows keep loading.
func TestSQLiteDueMigration(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	// Pre-M4 table: no due column at all.
	old, err := storage.NewSQLiteStorage(db, storage.WithDueColumn(""))
	if err != nil {
		t.Fatalf("NewSQLiteStorage(old): %v", err)
	}
	if err := old.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("Initialize(old schema): %v", err)
	}
	if _, err := old.SaveState(ctx, "legacy", workflow.NewMarking([]workflow.Place{"submitted"}), nil, 0); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	// Upgrade: a default store (due_at) migrates the table in place.
	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema (migration): %v", err)
	}
	// Idempotent: a second call must not fail on the already-present column.
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema (second call): %v", err)
	}

	// The legacy row still loads and, with a NULL due, never matches ListDue.
	if _, _, _, err := store.LoadState(ctx, "legacy"); err != nil {
		t.Fatalf("load legacy row after migration: %v", err)
	}
	if ids, err := store.ListDue(ctx, e2eEpoch.Add(1000*time.Hour), 0); err != nil {
		t.Fatalf("ListDue: %v", err)
	} else if len(ids) != 0 {
		t.Fatalf("ListDue = %v, want none (legacy row has NULL due)", ids)
	}

	// A due-aware save on the migrated table populates the index.
	due := e2eEpoch.Add(time.Hour)
	if _, err := store.SaveStateWithDue(ctx, "timed", workflow.NewMarking([]workflow.Place{"submitted"}), nil, 0, &due); err != nil {
		t.Fatalf("SaveStateWithDue: %v", err)
	}
	if ids, err := store.ListDue(ctx, due, 0); err != nil {
		t.Fatalf("ListDue(after due save): %v", err)
	} else if want := []string{"timed"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ListDue = %v, want %v", ids, want)
	}
}

// TestSQLiteDueOverflowClamp proves a due time beyond the int64 UnixNano range
// (after ~year 2262) saturates to math.MaxInt64 rather than wrapping negative:
// a far-future due must not appear in a present-day ListDue scan, and a clamped
// `before` behaves sanely.
func TestSQLiteDueOverflowClamp(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	// A due far past the UnixNano ceiling. Naively, UnixNano would wrap to a
	// negative value and this instance would look overdue "in the past".
	yr2266 := time.Date(2266, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := store.SaveStateWithDue(ctx, "far", workflow.NewMarking([]workflow.Place{"submitted"}), nil, 0, &yr2266); err != nil {
		t.Fatalf("SaveStateWithDue(far): %v", err)
	}

	// A present-day scan must NOT return the far-future instance.
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if ids, err := store.ListDue(ctx, now, 0); err != nil {
		t.Fatalf("ListDue(now): %v", err)
	} else if len(ids) != 0 {
		t.Fatalf("ListDue(now) = %v, want none (far-future due must not wrap negative)", ids)
	}

	// A `before` past the ceiling clamps too, and behaves sanely: it lists the
	// far-future instance (whose due clamped to the same ceiling) rather than
	// erroring or wrapping.
	yr2300 := time.Date(2300, 1, 1, 0, 0, 0, 0, time.UTC)
	if ids, err := store.ListDue(ctx, yr2300, 0); err != nil {
		t.Fatalf("ListDue(2300): %v", err)
	} else if want := []string{"far"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ListDue(2300) = %v, want %v (clamped before lists the clamped due)", ids, want)
	}
}

// TestSQLiteDueDisabled proves WithDueColumn("") turns the due index off: the
// schema omits the column, EnsureSchema is a no-op past the table create, and
// ListDue reports the missing capability.
func TestSQLiteDueDisabled(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	store, err := storage.NewSQLiteStorage(db, storage.WithDueColumn(""))
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if strings.Contains(store.GenerateSchema(), "due_at") {
		t.Fatalf("schema should omit the due column when disabled:\n%s", store.GenerateSchema())
	}
	// EnsureSchema still creates the table and is idempotent with the index off.
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema (second call): %v", err)
	}
	// The store still works for plain saves...
	if _, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"submitted"}), nil, 0); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	// ...but a fleet scan is unavailable.
	if _, err := store.ListDue(ctx, e2eEpoch, 0); err == nil {
		t.Fatal("ListDue on a due-disabled store: want error, got nil")
	}
}

// TestSQLiteFleetEscalation is the end-to-end acceptance for M4.4: a fleet of
// persisted instances with a 3-day escalation timer, a single indexed ListDue
// scan that finds exactly the overdue ones, FireDue that escalates them, an
// idempotent second FireDue, and a rescan that no longer returns them. All
// against fixed clocks — no sleeping.
func TestSQLiteFleetEscalation(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	const escalateAfter = 72 * time.Hour // "escalate if not approved in 3 days"
	def := escalationDef(t, escalateAfter)

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	mgr := workflow.NewManager(workflow.NewRegistry(), store)

	// The host's evaluation clock.
	now := e2eEpoch.Add(escalateAfter)

	// Three instances submitted at the epoch are overdue at `now`; two submitted
	// two days later are not. One overdue instance gets approved before the scan,
	// so it must drop out of the due index entirely.
	overdue := []string{"old-a", "old-b", "old-c"}
	for _, id := range overdue {
		seedSubmitted(t, mgr, id, def, e2eEpoch)
	}
	for _, id := range []string{"fresh-x", "fresh-y"} {
		seedSubmitted(t, mgr, id, def, e2eEpoch.Add(48*time.Hour))
	}
	seedSubmitted(t, mgr, "approved-one", def, e2eEpoch)
	if err := mgr.Execute(ctx, "approved-one", def, func(w *workflow.Workflow) error {
		return w.ApplyTransition("approve")
	}); err != nil {
		t.Fatalf("approve approved-one: %v", err)
	}

	// The cron scan: a single indexed query returns exactly the overdue instances,
	// ordered — not the fresh ones, not the approved one.
	dueIDs, err := mgr.ListDue(ctx, now, 0)
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if !reflect.DeepEqual(dueIDs, overdue) {
		t.Fatalf("ListDue = %v, want %v (exactly the overdue instances)", dueIDs, overdue)
	}

	// The ~10-line host cron: escalate each due instance.
	for _, id := range dueIDs {
		fired, err := mgr.FireDue(ctx, id, def, now)
		if err != nil {
			t.Fatalf("FireDue(%s): %v", id, err)
		}
		if len(fired) != 1 || fired[0] != "escalate" {
			t.Fatalf("FireDue(%s) = %v, want [escalate]", id, fired)
		}
		wf, err := mgr.LoadWorkflow(ctx, id, def)
		if err != nil {
			t.Fatalf("LoadWorkflow(%s): %v", id, err)
		}
		if !wf.Marking().HasPlace("escalated") {
			t.Fatalf("%s marking = %v, want [escalated]", id, wf.CurrentPlaces())
		}
	}

	// A second sweep at the same clock is a no-op: nothing overdue remains.
	rescan, err := mgr.ListDue(ctx, now, 0)
	if err != nil {
		t.Fatalf("ListDue(rescan): %v", err)
	}
	if len(rescan) != 0 {
		t.Fatalf("ListDue after escalation = %v, want none", rescan)
	}
	for _, id := range overdue {
		fired, err := mgr.FireDue(ctx, id, def, now)
		if err != nil {
			t.Fatalf("FireDue(%s, second): %v", id, err)
		}
		if len(fired) != 0 {
			t.Fatalf("second FireDue(%s) = %v, want none (idempotent)", id, fired)
		}
	}

	// The fresh instances become due once the clock advances past their deadline.
	later := e2eEpoch.Add(48 * time.Hour).Add(escalateAfter)
	if ids, err := mgr.ListDue(ctx, later, 0); err != nil {
		t.Fatalf("ListDue(later): %v", err)
	} else if want := []string{"fresh-x", "fresh-y"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ListDue(later) = %v, want %v", ids, want)
	}
}
