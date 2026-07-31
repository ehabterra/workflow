// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

// txGuardNet is the shape #35 exists for: whether the transition may fire is a
// fact about HOST data, not about the marking. Before tx-scoped guards the host
// had to read that fact, hand it in as a context boolean, and hope nothing
// changed before the save.
//
//	publish : draft -> published    tx_guard: "approvedCount() >= 2"
//
// `approvedCount` reads the host's own table through the firing transaction.
func txGuardNet(t *testing.T, expr string, builder workflow.TxEnvBuilder) *workflow.Definition {
	t.Helper()
	publish := workflow.MustNewTransition("publish",
		[]workflow.Place{"draft"}, []workflow.Place{"published"})
	c, err := workflow.NewTxExpressionConstraint(expr, builder)
	if err != nil {
		t.Fatal(err)
	}
	publish.AddConstraint(c)
	publish.SetMetadata("tx_guard", expr)

	def, err := workflow.NewDefinition(
		[]workflow.Place{"draft", "published"},
		[]workflow.Transition{*publish},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// txGuardBackend wires a real SQLite backend plus the host table the guard
// reads, and the environment that reads it. The environment queries through the
// transaction it is handed — not through the *sql.DB — which is the whole point:
// it sees the state this cycle will commit against.
func txGuardBackend(t *testing.T) (*storage.SQLiteStorage, *sql.DB, workflow.TxEnvBuilder) {
	t.Helper()
	ctx := context.Background()

	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // one shared in-memory database behind one lock

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE approvals (doc TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	env := func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		sqlTx := tx.(*sql.Tx)
		return map[string]any{
			"approvedCount": func() int {
				var n int
				if err := sqlTx.QueryRowContext(ctx,
					`SELECT COUNT(*) FROM approvals WHERE doc = ?`, ev.Workflow().Name()).Scan(&n); err != nil {
					return -1
				}
				return n
			},
		}
	}
	return store, db, env
}

// txGuardFixture is txGuardBackend plus the single-transition net most of these
// tests use.
func txGuardFixture(t *testing.T, expr string) (*workflow.Manager, *workflow.Definition, *sql.DB) {
	t.Helper()
	store, db, env := txGuardBackend(t)
	return workflow.NewManager(workflow.NewRegistry(), store), txGuardNet(t, expr, env), db
}

func approveDoc(t *testing.T, db *sql.DB, doc string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO approvals (doc) VALUES (?)`, doc); err != nil {
		t.Fatal(err)
	}
}

// TestTxGuardReadsHostStateInsideTheFiringTransaction is the headline: the guard
// consults the host's own table, with no value pre-computed into the context.
func TestTxGuardReadsHostStateInsideTheFiringTransaction(t *testing.T) {
	ctx := context.Background()
	mgr, def, db := txGuardFixture(t, "approvedCount() >= 2")

	if _, err := mgr.CreateWorkflow(ctx, "doc-1", def, "draft"); err != nil {
		t.Fatal(err)
	}

	fire := func() error {
		return mgr.Execute(ctx, "doc-1", def, func(wf *workflow.Workflow) error {
			return wf.ApplyTransitionWithContext(ctx, "publish")
		})
	}

	// One approval is not enough — and the rejection rolls the cycle back, so
	// nothing is persisted.
	approveDoc(t, db, "doc-1")
	if err := fire(); !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("one approval: want ErrGuardRejected, got %v", err)
	}
	wf, err := mgr.LoadWorkflow(ctx, "doc-1", def)
	if err != nil {
		t.Fatal(err)
	}
	if places := wf.CurrentPlaces(); len(places) != 1 || places[0] != "draft" {
		t.Fatalf("a rejected firing must not persist, got %v", places)
	}

	// The second approval flips the answer with no change to the workflow.
	approveDoc(t, db, "doc-1")
	if err := fire(); err != nil {
		t.Fatalf("two approvals: %v", err)
	}
	wf, err = mgr.LoadWorkflow(ctx, "doc-1", def)
	if err != nil {
		t.Fatal(err)
	}
	if places := wf.CurrentPlaces(); len(places) != 1 || places[0] != "published" {
		t.Fatalf("want [published], got %v", places)
	}
}

// TestTxGuardRejectsWhenTheInputChangedAfterThePreRead is the acceptance
// criterion of #35, stated exactly: a host that resolved its decision BEFORE the
// transaction opened would fire on a value that is no longer true. The
// tx-scoped guard re-reads inside the transaction and refuses.
func TestTxGuardRejectsWhenTheInputChangedAfterThePreRead(t *testing.T) {
	ctx := context.Background()
	mgr, def, db := txGuardFixture(t, "approvedCount() >= 2")

	if _, err := mgr.CreateWorkflow(ctx, "doc-2", def, "draft"); err != nil {
		t.Fatal(err)
	}
	approveDoc(t, db, "doc-2")
	approveDoc(t, db, "doc-2")

	// The host reads its precondition here — two approvals, publish is legal —
	// and this is the value a pre-#35 implementation would have injected into
	// the workflow context and fired on.
	var preRead int
	if err := db.QueryRow(`SELECT COUNT(*) FROM approvals WHERE doc = 'doc-2'`).Scan(&preRead); err != nil {
		t.Fatal(err)
	}
	if preRead < 2 {
		t.Fatalf("fixture: want the pre-read to say publish is legal, got %d", preRead)
	}

	// ...and then an approval is withdrawn before the fire.
	if _, err := db.Exec(`DELETE FROM approvals WHERE doc = 'doc-2'`); err != nil {
		t.Fatal(err)
	}

	err := mgr.Execute(ctx, "doc-2", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "publish")
	})
	if !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("the guard must re-decide inside the transaction, got %v", err)
	}

	wf, err := mgr.LoadWorkflow(ctx, "doc-2", def)
	if err != nil {
		t.Fatal(err)
	}
	if places := wf.CurrentPlaces(); len(places) != 1 || places[0] != "draft" {
		t.Fatalf("stale pre-read must not publish, got %v", places)
	}
}

// TestTxGuardDecidesBeforeEffectsRun pins the phase ordering, which matters now
// that guards and effects share one transaction: the guard still runs during the
// fire, BEFORE any effect writes. An effect cannot make a guard true.
func TestTxGuardDecidesBeforeEffectsRun(t *testing.T) {
	ctx := context.Background()
	mgr, def, db := txGuardFixture(t, "approvedCount() >= 2")

	if _, err := mgr.CreateWorkflow(ctx, "doc-3", def, "draft"); err != nil {
		t.Fatal(err)
	}
	approveDoc(t, db, "doc-3")

	// An effect writes the second approval into the cycle's own transaction. If
	// effects ran before the decision, the guard would see two and publish.
	err := mgr.Execute(ctx, "doc-3", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "publish")
	}, workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
		_, err := tx.(*sql.Tx).ExecContext(ctx, `INSERT INTO approvals (doc) VALUES ('doc-3')`)
		return err
	}))
	// The guard ran before the effect, so it still saw one approval: effects are
	// a post-decision phase, and this pins that ordering.
	if !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("guards decide before effects run, got %v", err)
	}

	// And because the whole cycle rolled back, the effect's insert is gone too.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM approvals WHERE doc = 'doc-3'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the rolled-back cycle left %d approvals, want 1", n)
	}
}

// TestTxGuardRoutesApplyAny: because the WHOLE fire happens inside the
// transaction, a tx-scoped guard can route an ApplyAny branch — not merely veto
// a firing that was already chosen.
func TestTxGuardRoutesApplyAny(t *testing.T) {
	ctx := context.Background()
	store, db, env := txGuardBackend(t)
	mustTx := func(e string) *workflow.ExpressionConstraint {
		c, err := workflow.NewTxExpressionConstraint(e, env)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	full := workflow.MustNewTransition("publish_full", []workflow.Place{"draft"}, []workflow.Place{"published"})
	full.AddConstraint(mustTx("approvedCount() >= 2"))
	full.SetMetadata("tx_guard", "approvedCount() >= 2")
	partial := workflow.MustNewTransition("publish_preview", []workflow.Place{"draft"}, []workflow.Place{"preview"})
	partial.AddConstraint(mustTx("approvedCount() >= 1"))
	partial.SetMetadata("tx_guard", "approvedCount() >= 1")

	def, err := workflow.NewDefinition(
		[]workflow.Place{"draft", "preview", "published"},
		[]workflow.Transition{*full, *partial},
	)
	if err != nil {
		t.Fatal(err)
	}
	mgr := workflow.NewManager(workflow.NewRegistry(), store)
	if _, err := mgr.CreateWorkflow(ctx, "doc-4", def, "draft"); err != nil {
		t.Fatal(err)
	}

	approveDoc(t, db, "doc-4")
	var fired string
	if err := mgr.Execute(ctx, "doc-4", def, func(wf *workflow.Workflow) error {
		var err error
		fired, err = wf.ApplyAny(ctx, "publish_full", "publish_preview")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fired != "publish_preview" {
		t.Fatalf("one approval should route to the preview branch, got %q", fired)
	}
}

// conflictOnce wraps a TxScopedStorage and fails the FIRST scoped save with
// ErrConflict, so Execute's retry path can be exercised deterministically. A
// real contending writer cannot be used here: the scope holds SQLite's single
// writer for the whole cycle, so a second writer would block rather than race.
type conflictOnce struct {
	workflow.TxScopedDueStorage
	scopes int
	failed bool
}

func (c *conflictOnce) BeginScope(ctx context.Context, fn func(context.Context, any) error) error {
	c.scopes++
	return c.TxScopedDueStorage.BeginScope(ctx, fn)
}

func (c *conflictOnce) SaveStateScoped(ctx context.Context, tx any, id string, m workflow.Marking, ctxData map[string]any, expected int64) (int64, error) {
	if c.conflict() {
		return 0, workflow.ErrConflict
	}
	return c.TxScopedDueStorage.SaveStateScoped(ctx, tx, id, m, ctxData, expected)
}

// SaveStateScopedWithDue must be wrapped too: the Manager prefers it over
// SaveStateScoped for any backend that maintains a due index, so overriding only
// the plain one would silently never be called.
func (c *conflictOnce) SaveStateScopedWithDue(ctx context.Context, tx any, id string, m workflow.Marking, ctxData map[string]any, expected int64, due *time.Time) (int64, error) {
	if c.conflict() {
		return 0, workflow.ErrConflict
	}
	return c.TxScopedDueStorage.SaveStateScopedWithDue(ctx, tx, id, m, ctxData, expected, due)
}

func (c *conflictOnce) conflict() bool {
	if c.failed {
		return false
	}
	c.failed = true
	return true
}

// TestTxGuardRetriesReDecideOnEachAttempt: Execute retries the whole cycle on
// ErrConflict, and each attempt opens a NEW transaction — so the guard is
// evaluated again rather than reusing an answer from the losing attempt, which
// was computed against state that did not commit.
func TestTxGuardRetriesReDecideOnEachAttempt(t *testing.T) {
	ctx := context.Background()
	store, db, env := txGuardBackend(t)
	def := txGuardNet(t, "approvedCount() >= 2", env)

	wrapped := &conflictOnce{TxScopedDueStorage: store}
	mgr := workflow.NewManager(workflow.NewRegistry(), wrapped)
	if _, err := mgr.CreateWorkflow(ctx, "doc-5", def, "draft"); err != nil {
		t.Fatal(err)
	}
	approveDoc(t, db, "doc-5")
	approveDoc(t, db, "doc-5")

	var mu sync.Mutex
	evaluations := 0
	def.AddGuardEventListener(func(ev *workflow.GuardEvent) error {
		mu.Lock()
		evaluations++
		mu.Unlock()
		return nil
	})

	if err := mgr.Execute(ctx, "doc-5", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "publish")
	}); err != nil {
		t.Fatalf("execute should succeed on the retry: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if evaluations != 2 {
		t.Fatalf("guard evaluations = %d, want 2 (one per attempt)", evaluations)
	}
	if wrapped.scopes != 2 {
		t.Fatalf("scopes opened = %d, want 2 — each attempt must get its own transaction", wrapped.scopes)
	}

	// The winning attempt committed.
	wf, err := mgr.LoadWorkflow(ctx, "doc-5", def)
	if err != nil {
		t.Fatal(err)
	}
	if places := wf.CurrentPlaces(); len(places) != 1 || places[0] != "published" {
		t.Fatalf("want [published], got %v", places)
	}
}

// --- refusals ---

// TestTxGuardOutsideATransactionRefuses: a guard that exists because staleness
// is wrong must not answer from stale state when nobody gave it a transaction.
func TestTxGuardOutsideATransactionRefuses(t *testing.T) {
	def := txGuardNet(t, "ok()", func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		return map[string]any{"ok": func() bool { return true }}
	})
	wf, err := workflow.NewWorkflow("bare", def, "draft")
	if err != nil {
		t.Fatal(err)
	}

	if err := wf.ApplyTransition("publish"); !errors.Is(err, workflow.ErrNoTransaction) {
		t.Fatalf("bare Apply: want ErrNoTransaction, got %v", err)
	}
	if err := wf.CanTransition("publish"); !errors.Is(err, workflow.ErrNoTransaction) {
		t.Fatalf("bare Can: want ErrNoTransaction, got %v", err)
	}
	if places := wf.CurrentPlaces(); len(places) != 1 || places[0] != "draft" {
		t.Fatalf("nothing should have moved, got %v", places)
	}
}

// TestTxGuardOnNonScopedStorageRefuses: the capability is checked up front,
// before anything fires, so an unsupported backend is a clear error rather than
// a guard failure halfway through.
func TestTxGuardOnNonScopedStorageRefuses(t *testing.T) {
	ctx := context.Background()
	def := txGuardNet(t, "ok()", func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		return map[string]any{"ok": func() bool { return true }}
	})
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore())
	if _, err := mgr.CreateWorkflow(ctx, "w", def, "draft"); err != nil {
		t.Fatal(err)
	}

	err := mgr.Execute(ctx, "w", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "publish")
	})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("want errors.ErrUnsupported, got %v", err)
	}
	if !strings.Contains(err.Error(), "TxScopedStorage") {
		t.Errorf("the error should name the missing capability, got %q", err)
	}
}

func TestNewTxExpressionConstraintRejectsMalformed(t *testing.T) {
	builder := func(ctx context.Context, tx any, ev workflow.Event) map[string]any { return nil }
	if _, err := workflow.NewTxExpressionConstraint("", builder); err == nil {
		t.Error("empty expression should be rejected")
	}
	if _, err := workflow.NewTxExpressionConstraint("ok()", nil); err == nil {
		t.Error("nil builder should be rejected")
	}
	if _, err := workflow.NewTxExpressionConstraint("len(", builder); err == nil {
		t.Error("uncompilable expression should be rejected")
	}
}

// TestTxGuardIsFingerprintedAndDiagrammed: a tx guard decides when the net may
// fire, so it is structure — and it must be visible, since a reader cannot see
// it in the arcs.
func TestTxGuardIsFingerprintedAndDiagrammed(t *testing.T) {
	builder := func(ctx context.Context, tx any, ev workflow.Event) map[string]any { return nil }
	build := func(expr string) *workflow.Definition {
		t.Helper()
		tr := workflow.MustNewTransition("publish", []workflow.Place{"draft"}, []workflow.Place{"published"})
		if expr != "" {
			c, err := workflow.NewTxExpressionConstraint(expr, builder)
			if err != nil {
				t.Fatal(err)
			}
			tr.AddConstraint(c)
			tr.SetMetadata("tx_guard", expr)
		}
		def, err := workflow.NewDefinition(
			[]workflow.Place{"draft", "published"}, []workflow.Transition{*tr})
		if err != nil {
			t.Fatal(err)
		}
		return def
	}

	none, one, other := build("").Fingerprint(), build("a()").Fingerprint(), build("b()").Fingerprint()
	if one == none {
		t.Error("adding a tx guard must move the fingerprint")
	}
	if one == other {
		t.Error("changing the tx guard expression must move the fingerprint")
	}
	if one != build("a()").Fingerprint() {
		t.Error("fingerprints must be stable")
	}

	diagram := build("approvedCount() >= 2").Diagram()
	if !strings.Contains(diagram, "⛁") || !strings.Contains(diagram, "approvedCount()") {
		t.Errorf("the diagram should show the tx guard:\n%s", diagram)
	}
}

// TestTxGuardMigrationRunsOutsideTheScope: the migration handler is host code
// that may write to storage, so consulting it while our own transaction is open
// on the same rows would deadlock. It runs before the scope instead.
func TestTxGuardMigrationRunsOutsideTheScope(t *testing.T) {
	ctx := context.Background()
	mgr, def, db := txGuardFixture(t, "approvedCount() >= 1")

	// Persist under one definition...
	if _, err := mgr.CreateWorkflow(ctx, "doc-6", def, "draft"); err != nil {
		t.Fatal(err)
	}
	approveDoc(t, db, "doc-6")

	// ...then drive it with a structurally different one.
	changed := txGuardNet(t, "approvedCount() >= 1", func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		return map[string]any{"approvedCount": func() int { return 1 }}
	})
	changed.Places = append(changed.Places, "archived")
	if changed.Fingerprint() == def.Fingerprint() {
		t.Fatal("fixture: the two definitions should differ structurally")
	}

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	var seen *workflow.DefinitionDiff
	migrating := workflow.NewManager(workflow.NewRegistry(), store,
		workflow.WithDefinitionMigration(func(ctx context.Context, mismatch workflow.DefinitionMismatch) error {
			seen = mismatch.Diff
			return nil // approve
		}))

	if err := migrating.Execute(ctx, "doc-6", changed, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "publish")
	}); err != nil {
		t.Fatalf("execute after approved migration: %v", err)
	}
	if seen == nil {
		t.Fatal("the migration handler was never consulted")
	}
	if len(seen.PlacesAdded) != 1 || seen.PlacesAdded[0] != "archived" {
		t.Errorf("diff should report the added place, got %v", seen)
	}
}

// TestTxGuardEnvIsAddedToTheStandardEnvironment: a tx guard is nearly always a
// comparison between something read live and something the host passed in, so
// the builder's entries are added to the usual environment rather than
// replacing it. (Found by using the feature: replacing it turned every such
// guard into a silent nil comparison.)
func TestTxGuardEnvIsAddedToTheStandardEnvironment(t *testing.T) {
	ctx := context.Background()
	mgr, def, db := txGuardFixture(t, "approvedCount() >= threshold")

	if _, err := mgr.CreateWorkflow(ctx, "doc-7", def, "draft"); err != nil {
		t.Fatal(err)
	}
	approveDoc(t, db, "doc-7")

	// `threshold` is a plain context value; `approvedCount()` comes from the
	// builder. Both must resolve in the same expression.
	if err := mgr.Execute(ctx, "doc-7", def, func(wf *workflow.Workflow) error {
		wf.SetContext("threshold", 5)
		return wf.ApplyTransitionWithContext(ctx, "publish")
	}); !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("threshold 5: want ErrGuardRejected, got %v", err)
	}
	if err := mgr.Execute(ctx, "doc-7", def, func(wf *workflow.Workflow) error {
		wf.SetContext("threshold", 1)
		return wf.ApplyTransitionWithContext(ctx, "publish")
	}); err != nil {
		t.Fatalf("threshold 1: %v", err)
	}
}
