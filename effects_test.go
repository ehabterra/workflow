// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/ehabterra/workflow"
)

// txMemStore is an in-memory TransactionalStorage that models the atomicity
// contract honestly: effects run against a pending buffer, and the state row is
// published only if every one of them succeeded. An effect that errors leaves
// both the state and the other effects' writes discarded.
type txMemStore struct {
	mu      sync.Mutex
	rows    map[string]txRow
	journal []string // committed effect writes, in order
}

type txRow struct {
	// state is SERIALIZED, not the live Marking. A fake that stores the object
	// hands the next load an alias the workflow then mutates in place, so a
	// failed save appears to have advanced the state. Real backends serialize;
	// so must this one.
	state   []byte
	ctx     map[string]any
	version int64
}

// fakeTx is the handle effects receive. Writes land here and are published to
// the store's journal only on commit.
type fakeTx struct{ pending []string }

func (t *fakeTx) write(s string) { t.pending = append(t.pending, s) }

func newTxMemStore() *txMemStore { return &txMemStore{rows: make(map[string]txRow)} }

func (s *txMemStore) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, nil, 0, workflow.ErrWorkflowNotFound
	}
	m, err := workflow.UnmarshalMarkingJSON(row.state)
	if err != nil {
		return nil, nil, 0, err
	}
	return m, row.ctx, row.version, nil
}

func (s *txMemStore) SaveState(ctx context.Context, id string, m workflow.Marking, c map[string]any, expected int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(id, m, c, expected)
}

func (s *txMemStore) saveLocked(id string, m workflow.Marking, c map[string]any, expected int64) (int64, error) {
	row, exists := s.rows[id]
	switch {
	case !exists && expected != 0:
		return 0, workflow.ErrConflict
	case exists && row.version != expected:
		return 0, workflow.ErrConflict
	}
	state, err := json.Marshal(m)
	if err != nil {
		return 0, err
	}
	next := expected + 1
	s.rows[id] = txRow{state: state, ctx: c, version: next}
	return next, nil
}

// SaveStateInTx runs the effects first, then publishes state and effect writes
// together — or neither.
func (s *txMemStore) SaveStateInTx(ctx context.Context, id string, m workflow.Marking, c map[string]any, expected int64, effects ...workflow.TxSideEffect) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx := &fakeTx{}
	for _, e := range effects {
		if err := e(ctx, tx); err != nil {
			return 0, err // roll back: nothing is published
		}
	}
	v, err := s.saveLocked(id, m, c, expected)
	if err != nil {
		return 0, err
	}
	s.journal = append(s.journal, tx.pending...)
	return v, nil
}

func (s *txMemStore) DeleteState(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

func (s *txMemStore) committed() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.journal...)
}

func mustGuard(t *testing.T, expr string) workflow.Constraint {
	t.Helper()
	c, err := workflow.NewExpressionConstraint(expr)
	if err != nil {
		t.Fatalf("guard %q: %v", expr, err)
	}
	return c
}

func decl(name string, params map[string]any) workflow.EffectDecl {
	return workflow.EffectDecl{Name: name, Params: params}
}

// effectDef builds a two-branch definition: approve_partial loops back on
// `open`, approve_final advances to `done`. Each carries different effects, so
// the branch that fires determines what runs.
func effectDef(t *testing.T) *workflow.Definition {
	t.Helper()
	partial := workflow.MustNewTransition("approve_partial", []workflow.Place{"open"}, []workflow.Place{"open"})
	partial.AddConstraint(mustGuard(t, "!satisfied"))
	partial.SetEffects(decl("audit", map[string]any{"detail": "pending"}))

	final := workflow.MustNewTransition("approve_final", []workflow.Place{"open"}, []workflow.Place{"done"})
	final.AddConstraint(mustGuard(t, "satisfied"))
	final.SetEffects(
		decl("audit", map[string]any{"detail": "complete"}),
		decl("outbox", map[string]any{"event": "approved"}),
	)
	final.SetAfterCommit(decl("email", map[string]any{"template": "approved"}))

	def, err := workflow.NewDefinition([]workflow.Place{"open", "done"}, []workflow.Transition{*partial, *final})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// recordingRegistry registers audit/outbox/email implementations that append to
// a shared journal so tests can assert order.
func recordingRegistry(sent *[]string) *workflow.EffectRegistry {
	reg := workflow.NewEffectRegistry()
	reg.MustRegister("audit", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		tx.(*fakeTx).write(fmt.Sprintf("audit:%v:%s", ev.Params["detail"], ev.Transition))
		return nil
	})
	reg.MustRegister("outbox", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		tx.(*fakeTx).write(fmt.Sprintf("outbox:%v", ev.Params["event"]))
		return nil
	})
	reg.MustRegisterAfterCommit("email", func(ctx context.Context, ev workflow.EffectEvent) error {
		*sent = append(*sent, fmt.Sprintf("email:%v", ev.Params["template"]))
		return nil
	})
	return reg
}

// TestFingerprintStableWithoutEffects is the compatibility guard: a definition
// declaring no effects must fingerprint exactly as it did before effects
// existed, or every instance persisted by an earlier version fails to load.
// Adding an effect must move it; clearing the effect must restore it.
func TestFingerprintStableWithoutEffects(t *testing.T) {
	build := func(effects ...workflow.EffectDecl) string {
		tr := workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
		if len(effects) > 0 {
			tr.SetEffects(effects...)
		}
		def, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr})
		if err != nil {
			t.Fatalf("NewDefinition: %v", err)
		}
		return def.Fingerprint()
	}

	plain := build()
	if plain != build() {
		t.Fatal("fingerprint is not stable across identical builds")
	}
	if withEffect := build(decl("audit", nil)); withEffect == plain {
		t.Error("declaring an effect must change the fingerprint")
	}
	// Clearing effects must produce the ORIGINAL bytes again — this is what
	// proves the effects segment is absent (not merely empty) when unused.
	if again := build(); again != plain {
		t.Errorf("fingerprint drifted for an effect-free definition:\n got %s\nwant %s", again, plain)
	}
}

// TestFingerprintEffectOrderAndParams: order is execution order and params are
// structure, so both must move the fingerprint. (Places and arcs are sorted
// before hashing; effects deliberately are not.)
func TestFingerprintEffectOrderAndParams(t *testing.T) {
	build := func(decls ...workflow.EffectDecl) string {
		tr := workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
		tr.SetEffects(decls...)
		def, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr})
		if err != nil {
			t.Fatalf("NewDefinition: %v", err)
		}
		return def.Fingerprint()
	}
	a, b := decl("audit", nil), decl("outbox", nil)
	if build(a, b) == build(b, a) {
		t.Error("effect order must affect the fingerprint — it is the execution order")
	}
	if build(decl("audit", map[string]any{"d": "x"})) == build(decl("audit", map[string]any{"d": "y"})) {
		t.Error("effect params must affect the fingerprint")
	}
}

func TestEffectRegistryRejectsDuplicatesAndEmpty(t *testing.T) {
	reg := workflow.NewEffectRegistry()
	noop := func(context.Context, any, workflow.EffectEvent) error { return nil }

	if err := reg.Register("", noop); err == nil {
		t.Error("empty name should be rejected")
	}
	if err := reg.Register("audit", nil); err == nil {
		t.Error("nil implementation should be rejected")
	}
	if err := reg.Register("audit", noop); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := reg.Register("audit", noop); err == nil {
		t.Error("duplicate registration should be rejected — a silent overwrite " +
			"would make behaviour depend on startup order")
	}
}

// TestEffectRegistryValidate catches unregistered names at startup rather than
// when a rare branch first fires.
func TestEffectRegistryValidate(t *testing.T) {
	def := effectDef(t)
	reg := workflow.NewEffectRegistry()
	reg.MustRegister("audit", func(context.Context, any, workflow.EffectEvent) error { return nil })

	err := reg.Validate(def)
	if err == nil {
		t.Fatal("expected unregistered effects to be reported")
	}
	for _, want := range []string{"outbox", "email"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate error %q does not mention %q", err, want)
		}
	}

	reg.MustRegister("outbox", func(context.Context, any, workflow.EffectEvent) error { return nil })
	reg.MustRegisterAfterCommit("email", func(context.Context, workflow.EffectEvent) error { return nil })
	if err := reg.Validate(def); err != nil {
		t.Errorf("Validate after registering everything: %v", err)
	}
}

// TestPerBranchEffects is the headline behaviour: one action with two outcomes
// fires the effects of the branch that actually won, with no host-side switch.
func TestPerBranchEffects(t *testing.T) {
	ctx := context.Background()
	def := effectDef(t)
	store := newTxMemStore()
	var sent []string
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithEffectRegistry(recordingRegistry(&sent)))

	if _, err := mgr.CreateWorkflow(ctx, "w1", def, "open"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Branch 1: the chain is not yet satisfied — record and stay put.
	if err := mgr.Execute(ctx, "w1", def, func(wf *workflow.Workflow) error {
		wf.SetContext("satisfied", false) // chain not yet complete
		_, err := wf.ApplyAny(ctx, "approve_final", "approve_partial")
		return err
	}); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if got := store.committed(); len(got) != 1 || got[0] != "audit:pending:approve_partial" {
		t.Fatalf("after partial branch, journal = %v", got)
	}
	if len(sent) != 0 {
		t.Errorf("partial branch must not send mail, got %v", sent)
	}

	// Branch 2: satisfied — advance, with a different effect set plus after-commit.
	if err := mgr.Execute(ctx, "w1", def, func(wf *workflow.Workflow) error {
		wf.SetContext("satisfied", true) // the approval that completes the chain
		_, err := wf.ApplyAny(ctx, "approve_final", "approve_partial")
		return err
	}); err != nil {
		t.Fatalf("final: %v", err)
	}
	want := []string{"audit:pending:approve_partial", "audit:complete:approve_final", "outbox:approved"}
	got := store.committed()
	if len(got) != len(want) {
		t.Fatalf("journal = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("journal[%d] = %q, want %q (declared order is execution order)", i, got[i], want[i])
		}
	}
	if len(sent) != 1 || sent[0] != "email:approved" {
		t.Errorf("after-commit effects = %v, want [email:approved]", sent)
	}
}

// TestEffectFailureRollsBackState: an effect that errors aborts the transaction,
// so neither the state change nor a sibling effect's write survives.
func TestEffectFailureRollsBackState(t *testing.T) {
	ctx := context.Background()
	def := effectDef(t)
	store := newTxMemStore()

	reg := workflow.NewEffectRegistry()
	reg.MustRegister("audit", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		tx.(*fakeTx).write("audit")
		return nil
	})
	boom := errors.New("outbox unavailable")
	reg.MustRegister("outbox", func(context.Context, any, workflow.EffectEvent) error { return boom })
	reg.MustRegisterAfterCommit("email", func(context.Context, workflow.EffectEvent) error {
		t.Error("after-commit must not run when the transaction failed")
		return nil
	})
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithEffectRegistry(reg))

	if _, err := mgr.CreateWorkflow(ctx, "w2", def, "open"); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := mgr.Execute(ctx, "w2", def, func(wf *workflow.Workflow) error {
		wf.SetContext("satisfied", true)
		return wf.ApplyTransitionWithContext(ctx, "approve_final")
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got := store.committed(); len(got) != 0 {
		t.Errorf("sibling effect writes survived a failed transaction: %v", got)
	}
	marking, _, _, lerr := store.LoadState(ctx, "w2")
	if lerr != nil {
		t.Fatalf("load: %v", lerr)
	}
	if !marking.HasPlace("open") {
		t.Errorf("state advanced despite the effect failing: %v", marking.Places())
	}
}

// TestExecuteRequiresRegistry: a definition that declares effects must not run
// against a Manager with no registry — silently skipping writes the definition
// promised is worse than failing.
func TestExecuteRequiresRegistry(t *testing.T) {
	ctx := context.Background()
	def := effectDef(t)
	mgr := workflow.NewManager(workflow.NewRegistry(), newTxMemStore())
	if _, err := mgr.CreateWorkflow(ctx, "w3", def, "open"); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := mgr.Execute(ctx, "w3", def, func(wf *workflow.Workflow) error {
		wf.SetContext("satisfied", true)
		return wf.ApplyTransitionWithContext(ctx, "approve_final")
	})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "WithEffectRegistry") {
		t.Errorf("error should point at the fix, got: %v", err)
	}
}

// TestUnregisteredEffectFailsBeforeSave: resolution happens before the save, so
// a missing implementation aborts the attempt rather than tearing down a
// transaction with some effects already applied.
func TestUnregisteredEffectFailsBeforeSave(t *testing.T) {
	ctx := context.Background()
	def := effectDef(t)
	store := newTxMemStore()
	reg := workflow.NewEffectRegistry()
	reg.MustRegister("audit", func(context.Context, any, workflow.EffectEvent) error { return nil })
	// outbox deliberately not registered.
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithEffectRegistry(reg))

	if _, err := mgr.CreateWorkflow(ctx, "w4", def, "open"); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := mgr.Execute(ctx, "w4", def, func(wf *workflow.Workflow) error {
		wf.SetContext("satisfied", true)
		return wf.ApplyTransitionWithContext(ctx, "approve_final")
	})
	if err == nil || !strings.Contains(err.Error(), "unregistered effect") {
		t.Fatalf("err = %v, want an unregistered-effect error", err)
	}
	if got := store.committed(); len(got) != 0 {
		t.Errorf("effects ran despite an unresolvable sibling: %v", got)
	}
}

// TestEffectEventCarriesMarkingAndContext: an effect is told which transition
// fired, the marking either side of it, and the instance context.
func TestEffectEventCarriesMarkingAndContext(t *testing.T) {
	ctx := context.Background()
	def := effectDef(t)
	store := newTxMemStore()

	var seen workflow.EffectEvent
	reg := workflow.NewEffectRegistry()
	reg.MustRegister("audit", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		seen = ev
		return nil
	})
	reg.MustRegister("outbox", func(context.Context, any, workflow.EffectEvent) error { return nil })
	reg.MustRegisterAfterCommit("email", func(context.Context, workflow.EffectEvent) error { return nil })
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithEffectRegistry(reg))

	if _, err := mgr.CreateWorkflow(ctx, "w5", def, "open"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := mgr.Execute(ctx, "w5", def, func(wf *workflow.Workflow) error {
		wf.SetContext("actor", "sam")
		wf.SetContext("satisfied", true)
		return wf.ApplyTransitionWithContext(ctx, "approve_final")
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if seen.WorkflowID != "w5" || seen.Transition != "approve_final" {
		t.Errorf("event identity = %q/%q", seen.WorkflowID, seen.Transition)
	}
	if len(seen.Before) != 1 || seen.Before[0] != "open" {
		t.Errorf("Before = %v, want [open]", seen.Before)
	}
	if len(seen.After) != 1 || seen.After[0] != "done" {
		t.Errorf("After = %v, want [done]", seen.After)
	}
	if seen.Context["actor"] != "sam" {
		t.Errorf("Context[actor] = %v, want sam", seen.Context["actor"])
	}
	if seen.Params["detail"] != "complete" {
		t.Errorf("Params[detail] = %v, want complete", seen.Params["detail"])
	}
}
