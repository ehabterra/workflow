package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

func mkMarking(places ...workflow.Place) workflow.Marking {
	return workflow.NewMarking(places)
}

// newStateStore returns an initialized SQLite store on db.
func newStateStore(t *testing.T, db *sql.DB) *storage.SQLiteStorage {
	t.Helper()
	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return store
}

// Every save is versioned: an overwrite must carry the current version, bump
// it, and reject stale writers (regression heritage: REPLACE INTO used to
// delete and re-insert the row, silently resetting the version column).
func TestSaveState_VersionedOverwrite(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	store := newStateStore(t, db)

	if _, err := store.SaveState(ctx, "wf", mkMarking("a"), nil, 0); err != nil {
		t.Fatalf("create versioned: %v", err)
	}
	if _, err := store.SaveState(ctx, "wf", mkMarking("b"), nil, 1); err != nil {
		t.Fatalf("bump version: %v", err)
	}
	// A stale writer (version already consumed) must conflict, not clobber.
	if _, err := store.SaveState(ctx, "wf", mkMarking("x"), nil, 1); !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("stale overwrite err = %v, want ErrConflict", err)
	}

	if _, err := store.SaveState(ctx, "wf", mkMarking("c"), map[string]any{"k": "v"}, 2); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	m, ctxData, version, err := store.LoadState(ctx, "wf")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if version != 3 {
		t.Fatalf("version after SaveState = %d, want 3", version)
	}
	if !m.HasPlace("c") {
		t.Fatalf("marking = %v, want [c]", m.Places())
	}
	if ctxData["k"] != "v" {
		t.Fatalf("context k = %v, want v", ctxData["k"])
	}
}

// SaveStateInTx commits the state change and every side effect
// together — or neither.
func TestSaveStateInTx_Atomicity(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	store := newStateStore(t, db)
	hist := history.NewSQLiteHistory(db)
	if err := hist.Initialize(ctx); err != nil {
		t.Fatalf("init history: %v", err)
	}
	countHistory := func() int {
		records, err := hist.ListHistory(ctx, "wf", history.QueryOptions{})
		if err != nil {
			t.Fatalf("ListHistory: %v", err)
		}
		return len(records)
	}

	if _, err := store.SaveState(ctx, "wf", mkMarking("a"), nil, 0); err != nil {
		t.Fatalf("create: %v", err)
	}

	record := &history.TransitionRecord{WorkflowID: "wf", FromState: "a", ToState: "b", Transition: "go"}
	writeHistory := func(ctx context.Context, tx any) error {
		return hist.SaveTransitionTx(ctx, tx.(*sql.Tx), record)
	}

	// Success: state and history commit together.
	v, err := store.SaveStateInTx(ctx, "wf", mkMarking("b"), nil, 1, writeHistory)
	if err != nil {
		t.Fatalf("SaveStateInTx: %v", err)
	}
	if v != 2 {
		t.Fatalf("new version = %d, want 2", v)
	}
	if got := countHistory(); got != 1 {
		t.Fatalf("history rows = %d, want 1", got)
	}

	// Failing effect: the state change must roll back with it.
	boom := errors.New("effect exploded")
	_, err = store.SaveStateInTx(ctx, "wf", mkMarking("c"), nil, 2,
		writeHistory,
		func(context.Context, any) error { return boom },
	)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrap of the effect error", err)
	}
	m, _, version, err := store.LoadState(ctx, "wf")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if version != 2 || !m.HasPlace("b") {
		t.Fatalf("state after failed effect = %v v%d, want [b] v2 (rolled back)", m.Places(), version)
	}
	if got := countHistory(); got != 1 {
		t.Fatalf("history rows after rollback = %d, want 1 (second record rolled back)", got)
	}

	// Version conflict: save fails before effects; nothing is written.
	_, err = store.SaveStateInTx(ctx, "wf", mkMarking("c"), nil, 99, writeHistory)
	if !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("stale save err = %v, want ErrConflict", err)
	}
	if got := countHistory(); got != 1 {
		t.Fatalf("history rows after conflict = %d, want 1", got)
	}
}

// The *Tx method variants participate in a caller-managed transaction:
// uncommitted writes are visible inside it and discarded on rollback.
func TestTxMethodVariants_SQLite(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	store := newStateStore(t, db)

	// Rollback discards SaveStateTx.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := store.SaveStateTx(ctx, tx, "wf", mkMarking("a"), nil, 0); err != nil {
		t.Fatalf("SaveStateTx: %v", err)
	}
	// Inside the tx the write is visible…
	if m, _, _, err := store.LoadStateTx(ctx, tx, "wf"); err != nil || !m.HasPlace("a") {
		t.Fatalf("LoadStateTx = %v, %v; want marking [a]", m, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	// …and gone after rollback.
	if _, _, _, err := store.LoadState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("LoadState after rollback err = %v, want ErrWorkflowNotFound", err)
	}

	// Commit persists SaveStateTx and DeleteStateTx works in a tx.
	if err := storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		_, err := store.SaveStateTx(ctx, tx, "wf", mkMarking("b"), nil, 0)
		return err
	}); err != nil {
		t.Fatalf("RunInTx save: %v", err)
	}
	if m, _, _, err := store.LoadState(ctx, "wf"); err != nil || !m.HasPlace("b") {
		t.Fatalf("after commit: %v, %v; want [b]", m, err)
	}
	if err := storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		return store.DeleteStateTx(ctx, tx, "wf")
	}); err != nil {
		t.Fatalf("RunInTx delete: %v", err)
	}
	if _, _, _, err := store.LoadState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("after DeleteStateTx err = %v, want ErrWorkflowNotFound", err)
	}
}

// End-to-end: Manager.Execute + WithTxSideEffect gives a webhook handler
// crash-consistent state+history in a few lines.
func TestManagerExecute_AtomicHistorySideEffect(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)
	store := newStateStore(t, db)
	hist := history.NewSQLiteHistory(db)
	if err := hist.Initialize(ctx); err != nil {
		t.Fatalf("init history: %v", err)
	}

	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	mgr := workflow.NewManager(workflow.NewRegistry(), store)
	if _, err := mgr.CreateWorkflow(ctx, "wf", def, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	var record *history.TransitionRecord
	err = mgr.Execute(ctx, "wf", def,
		func(wf *workflow.Workflow) error {
			if err := wf.ApplyTransition("go"); err != nil {
				return err
			}
			record = &history.TransitionRecord{WorkflowID: "wf", FromState: "a", ToState: "b", Transition: "go"}
			return nil
		},
		workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
			return hist.SaveTransitionTx(ctx, tx.(*sql.Tx), record)
		}),
	)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wf, err := mgr.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if !wf.Marking().HasPlace("b") {
		t.Fatalf("marking = %v, want [b]", wf.CurrentPlaces())
	}
	records, err := hist.ListHistory(ctx, "wf", history.QueryOptions{})
	if err != nil {
		t.Fatalf("ListHistory: %v", err)
	}
	if len(records) != 1 || records[0].Transition != "go" {
		t.Fatalf("history = %+v, want one 'go' record", records)
	}

	// A failing effect aborts the whole Execute: state stays at b.
	err = mgr.Execute(ctx, "wf", def,
		func(wf *workflow.Workflow) error { return nil },
		workflow.WithTxSideEffect(func(context.Context, any) error {
			return fmt.Errorf("history table unavailable")
		}),
	)
	if err == nil {
		t.Fatal("Execute with failing effect should error")
	}

	// Executing with effects on a non-transactional storage fails loudly.
	plain := workflow.NewManager(workflow.NewRegistry(), nonTransactional{store})
	err = plain.Execute(ctx, "wf", def,
		func(wf *workflow.Workflow) error { return nil },
		workflow.WithTxSideEffect(func(context.Context, any) error { return nil }),
	)
	if err == nil {
		t.Fatal("Execute with effects on non-transactional storage should error")
	}
}

// nonTransactional hides the TransactionalStorage implementation of the
// embedded store, exposing only the plain Storage interface.
type nonTransactional struct{ inner workflow.Storage }

func (n nonTransactional) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	return n.inner.LoadState(ctx, id)
}

func (n nonTransactional) SaveState(ctx context.Context, id string, m workflow.Marking, c map[string]any, expectedVersion int64) (int64, error) {
	return n.inner.SaveState(ctx, id, m, c, expectedVersion)
}

func (n nonTransactional) DeleteState(ctx context.Context, id string) error {
	return n.inner.DeleteState(ctx, id)
}
