package workflow_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

// memDueStore is an in-memory workflow.DueStorage built on memStore, used to
// exercise the Manager's due-index behavior (FireDue self-heal) deterministically.
type memDueStore struct {
	*memStore
	dmu sync.Mutex
	due map[string]*time.Time
}

func newMemDueStore() *memDueStore {
	return &memDueStore{memStore: newMemStore(), due: make(map[string]*time.Time)}
}

func (s *memDueStore) SaveVersionedStateWithDue(ctx context.Context, id string, m workflow.Marking, c map[string]any, expected int64, due *time.Time) (int64, error) {
	v, err := s.SaveVersionedState(ctx, id, m, c, expected)
	if err != nil {
		return 0, err
	}
	s.dmu.Lock()
	s.due[id] = due
	s.dmu.Unlock()
	return v, nil
}

func (s *memDueStore) ListDue(ctx context.Context, before time.Time, limit int) ([]string, error) {
	s.dmu.Lock()
	defer s.dmu.Unlock()
	var ids []string
	for id, d := range s.due {
		if d != nil && !d.After(before) {
			ids = append(ids, id)
		}
	}
	// Mirror the DueStorage contract: due-time ascending, ID as tie-breaker.
	sort.Slice(ids, func(i, j int) bool {
		di, dj := s.due[ids[i]], s.due[ids[j]]
		if !di.Equal(*dj) {
			return di.Before(*dj)
		}
		return ids[i] < ids[j]
	})
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

func (s *memDueStore) storedDue(id string) (*time.Time, bool) {
	s.dmu.Lock()
	defer s.dmu.Unlock()
	d, ok := s.due[id]
	return d, ok
}

// partialDueTxStore is a backend that is a TransactionalStorage AND a DueStorage
// but deliberately NOT a TransactionalDueStorage (no SaveVersionedStateInTxWithDue).
// It is the exact shape Manager.Execute must reject for a timed definition with a
// side effect, since it cannot update the due index atomically with state+effect.
type partialDueTxStore struct {
	*memDueStore
}

func (s *partialDueTxStore) SaveVersionedStateInTx(ctx context.Context, id string, m workflow.Marking, c map[string]any, expected int64, effects ...workflow.TxSideEffect) (int64, error) {
	v, err := s.SaveVersionedState(ctx, id, m, c, expected)
	if err != nil {
		return 0, err
	}
	for _, e := range effects {
		if err := e(ctx, nil); err != nil {
			return 0, err
		}
	}
	return v, nil
}

// fireDueEpoch is a fixed reference time so every FireDue test is deterministic
// without ever sleeping.
var fireDueEpoch = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

func at(t time.Time) func() time.Time { return func() time.Time { return t } }

// escalationDef builds the canonical timed workflow:
// submitted --approve--> approved, and submitted --escalate(after)--> escalated.
// When guard is non-empty it is compiled onto the escalate transition.
func escalationDef(t *testing.T, after time.Duration, guard string) *workflow.Definition {
	t.Helper()
	approve := workflow.MustNewTransition("approve", []workflow.Place{"submitted"}, []workflow.Place{"approved"})
	escalate := workflow.MustNewTransition("escalate", []workflow.Place{"submitted"}, []workflow.Place{"escalated"})
	escalate.SetTimeoutAfter(after)
	if guard != "" {
		gc, err := workflow.NewExpressionConstraint(guard)
		if err != nil {
			t.Fatalf("compile guard %q: %v", guard, err)
		}
		escalate.AddConstraint(gc)
	}
	def, err := workflow.NewDefinition(
		[]workflow.Place{"submitted", "approved", "escalated"},
		[]workflow.Transition{*approve, *escalate},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// seedSubmitted persists a submitted instance whose token entered at t0 (so its
// escalation deadline is deterministic), with the given initial context.
func seedSubmitted(t *testing.T, mgr *workflow.Manager, id string, def *workflow.Definition, t0 time.Time, ctxData map[string]any) {
	t.Helper()
	wf, err := workflow.NewWorkflow(id, def, "submitted", workflow.WithClock(at(t0)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	wf.SetManager(mgr)
	for k, v := range ctxData {
		wf.SetContext(k, v)
	}
	if err := mgr.SaveWorkflow(context.Background(), id, wf); err != nil {
		t.Fatalf("SaveWorkflow(%s): %v", id, err)
	}
}

func TestFireDue_FiresDueTransitionAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	def := escalationDef(t, 72*time.Hour, "")
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore(), workflow.WithoutRegistryCache())
	seedSubmitted(t, mgr, "wf", def, fireDueEpoch, nil)

	// Before the deadline nothing fires.
	fired, err := mgr.FireDue(ctx, "wf", def, fireDueEpoch.Add(time.Hour))
	if err != nil {
		t.Fatalf("FireDue(before): %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("FireDue(before deadline) = %v, want none", fired)
	}

	// At the deadline escalate fires.
	fired, err = mgr.FireDue(ctx, "wf", def, fireDueEpoch.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("FireDue(at): %v", err)
	}
	if len(fired) != 1 || fired[0] != "escalate" {
		t.Fatalf("FireDue(at deadline) = %v, want [escalate]", fired)
	}
	wf, err := mgr.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if !wf.Marking().HasPlace("escalated") {
		t.Fatalf("marking = %v, want [escalated]", wf.CurrentPlaces())
	}

	// A second call, even far past the deadline, is a no-op.
	fired, err = mgr.FireDue(ctx, "wf", def, fireDueEpoch.Add(100*time.Hour))
	if err != nil {
		t.Fatalf("FireDue(second): %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("second FireDue = %v, want none (idempotent)", fired)
	}
}

func TestFireDue_SkipsGuardRejectedTransition(t *testing.T) {
	ctx := context.Background()
	def := escalationDef(t, 72*time.Hour, "getContext('ready', false) == true")
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore(), workflow.WithoutRegistryCache())
	seedSubmitted(t, mgr, "wf", def, fireDueEpoch, map[string]any{"ready": false})

	now := fireDueEpoch.Add(72 * time.Hour)

	// Due, but the guard blocks it: skipped, not an error, and the instance is
	// left untouched so a later scan can retry it.
	fired, err := mgr.FireDue(ctx, "wf", def, now)
	if err != nil {
		t.Fatalf("FireDue(blocked): %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("FireDue with blocking guard = %v, want none", fired)
	}
	wf, err := mgr.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if !wf.Marking().HasPlace("submitted") {
		t.Fatalf("marking = %v, want [submitted] (unchanged)", wf.CurrentPlaces())
	}

	// Open the guard, then the same deadline fires.
	if err := mgr.Execute(ctx, "wf", def, func(w *workflow.Workflow) error {
		w.SetContext("ready", true)
		return nil
	}); err != nil {
		t.Fatalf("open guard: %v", err)
	}
	fired, err = mgr.FireDue(ctx, "wf", def, now)
	if err != nil {
		t.Fatalf("FireDue(open): %v", err)
	}
	if len(fired) != 1 || fired[0] != "escalate" {
		t.Fatalf("FireDue after opening guard = %v, want [escalate]", fired)
	}
}

func TestFireDue_ProducedTokensStartFreshTimers(t *testing.T) {
	// a --t1(1h)--> b --t2(1h)--> c. Both timeouts elapsed relative to the seed
	// time, but firing t1 stamps b's token at `now`, so t2's deadline is now+1h —
	// t2 must NOT fire in the same pass (the clock is pinned to now).
	t1 := workflow.MustNewTransition("t1", []workflow.Place{"a"}, []workflow.Place{"b"})
	t1.SetTimeoutAfter(time.Hour)
	t2 := workflow.MustNewTransition("t2", []workflow.Place{"b"}, []workflow.Place{"c"})
	t2.SetTimeoutAfter(time.Hour)
	def, err := workflow.NewDefinition([]workflow.Place{"a", "b", "c"}, []workflow.Transition{*t1, *t2})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	ctx := context.Background()
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore(), workflow.WithoutRegistryCache())

	wf, err := workflow.NewWorkflow("wf", def, "a", workflow.WithClock(at(fireDueEpoch)))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	wf.SetManager(mgr)
	if err := mgr.SaveWorkflow(ctx, "wf", wf); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	now := fireDueEpoch.Add(10 * time.Hour) // well past both raw timeouts
	fired, err := mgr.FireDue(ctx, "wf", def, now)
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
	if len(fired) != 1 || fired[0] != "t1" {
		t.Fatalf("FireDue = %v, want [t1] only (t2's timer starts at now)", fired)
	}

	// One hour later t2 becomes due and fires.
	fired, err = mgr.FireDue(ctx, "wf", def, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("FireDue(+1h): %v", err)
	}
	if len(fired) != 1 || fired[0] != "t2" {
		t.Fatalf("FireDue(+1h) = %v, want [t2]", fired)
	}
}

func TestFireDue_FiresMultipleIndependentDue(t *testing.T) {
	// Two independent timers both overdue from the seeded marking fire in one pass.
	ta := workflow.MustNewTransition("ta", []workflow.Place{"p1"}, []workflow.Place{"x"})
	ta.SetTimeoutAfter(time.Hour)
	tb := workflow.MustNewTransition("tb", []workflow.Place{"p2"}, []workflow.Place{"y"})
	tb.SetTimeoutAfter(time.Hour)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"p1", "p2", "x", "y"},
		[]workflow.Transition{*ta, *tb},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	ctx := context.Background()
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore(), workflow.WithoutRegistryCache())

	wf, err := workflow.NewWorkflowFromMarking("wf", def, workflow.NewMarking([]workflow.Place{"p1", "p2"}), workflow.WithClock(at(fireDueEpoch)))
	if err != nil {
		t.Fatalf("NewWorkflowFromMarking: %v", err)
	}
	wf.SetManager(mgr)
	if err := mgr.SaveWorkflow(ctx, "wf", wf); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}

	fired, err := mgr.FireDue(ctx, "wf", def, fireDueEpoch.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
	if len(fired) != 2 {
		t.Fatalf("FireDue = %v, want both ta and tb", fired)
	}
	got := map[string]bool{fired[0]: true, fired[1]: true}
	if !got["ta"] || !got["tb"] {
		t.Fatalf("FireDue = %v, want [ta tb] in some order", fired)
	}
}

func TestExecute_RejectsPartialDueBackendWithEffect(t *testing.T) {
	ctx := context.Background()
	def := escalationDef(t, 72*time.Hour, "")
	store := &partialDueTxStore{memDueStore: newMemDueStore()}
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	seedSubmitted(t, mgr, "wf", def, fireDueEpoch, nil)

	// A timed definition + WithTxSideEffect on a DueStorage that is not a
	// TransactionalDueStorage must be rejected loudly, not silently corrupt the index.
	effectRan := false
	err := mgr.Execute(ctx, "wf", def, func(w *workflow.Workflow) error {
		return w.ApplyTransition("approve")
	}, workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
		effectRan = true
		return nil
	}))
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Execute err = %v, want ErrUnsupported (partial due backend rejected)", err)
	}
	if effectRan {
		t.Fatal("side effect ran despite the rejection; Execute must fail before saving")
	}

	// A timer-free definition on the same backend is unaffected (no due index to
	// corrupt), so the effect path works.
	approve := workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
	plainDef, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*approve})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	if _, err := mgr.CreateWorkflow(ctx, "plain", plainDef, "a"); err != nil {
		t.Fatalf("CreateWorkflow(plain): %v", err)
	}
	if err := mgr.Execute(ctx, "plain", plainDef, func(w *workflow.Workflow) error {
		return w.ApplyTransition("go")
	}, workflow.WithTxSideEffect(func(ctx context.Context, tx any) error { return nil })); err != nil {
		t.Fatalf("Execute(plain, timer-free) = %v, want nil (unaffected)", err)
	}
}

func TestFireDue_SkipsSaveWhenGuardBlockedIndexCorrect(t *testing.T) {
	ctx := context.Background()
	def := escalationDef(t, 72*time.Hour, "getContext('ready', false) == true")
	store := newMemStore()
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	seedSubmitted(t, mgr, "wf", def, fireDueEpoch, map[string]any{"ready": false})

	_, _, v0, err := store.LoadVersionedState(ctx, "wf")
	if err != nil {
		t.Fatalf("load initial version: %v", err)
	}

	now := fireDueEpoch.Add(72 * time.Hour) // escalate is due but guard-blocked
	for i := range 2 {
		fired, err := mgr.FireDue(ctx, "wf", def, now)
		if err != nil {
			t.Fatalf("FireDue(%d): %v", i, err)
		}
		if len(fired) != 0 {
			t.Fatalf("FireDue(%d) = %v, want none (guard-blocked)", i, fired)
		}
	}

	_, _, v1, err := store.LoadVersionedState(ctx, "wf")
	if err != nil {
		t.Fatalf("load final version: %v", err)
	}
	if v1 != v0 {
		t.Fatalf("version bumped from %d to %d; a due-but-blocked no-op must not save", v0, v1)
	}
}

func TestFireDue_SelfHealsStaleDueIndex(t *testing.T) {
	ctx := context.Background()
	def := escalationDef(t, 72*time.Hour, "")
	store := newMemDueStore()
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	seedSubmitted(t, mgr, "wf", def, fireDueEpoch, nil)

	// Advance to an approved (timer-free) state — the due index is correctly cleared.
	if err := mgr.Execute(ctx, "wf", def, func(w *workflow.Workflow) error {
		return w.ApplyTransition("approve")
	}); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// Simulate a bypass path that left a STALE due on a timer-free instance.
	stale := fireDueEpoch.Add(time.Hour)
	store.dmu.Lock()
	store.due["wf"] = &stale
	store.dmu.Unlock()

	_, _, vBefore, err := store.LoadVersionedState(ctx, "wf")
	if err != nil {
		t.Fatalf("load version before: %v", err)
	}

	// FireDue finds nothing due (approved has no timer). The live marking has no
	// running timer while the stored index says "due" — a stale index — so FireDue
	// must SAVE to self-heal (clear the due), even though nothing fired.
	now := fireDueEpoch.Add(100 * time.Hour)
	fired, err := mgr.FireDue(ctx, "wf", def, now)
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("FireDue = %v, want none", fired)
	}
	if d, _ := store.storedDue("wf"); d != nil {
		t.Fatalf("stored due = %v, want nil (index self-healed)", d)
	}
	_, _, vAfter, err := store.LoadVersionedState(ctx, "wf")
	if err != nil {
		t.Fatalf("load version after: %v", err)
	}
	if vAfter == vBefore {
		t.Fatalf("version unchanged at %d; a stale index must be saved to self-heal", vBefore)
	}
	if ids, err := store.ListDue(ctx, now, 0); err != nil {
		t.Fatalf("ListDue: %v", err)
	} else if len(ids) != 0 {
		t.Fatalf("ListDue = %v, want none (stale entry cleared)", ids)
	}
}

// BenchmarkFireDue measures a full FireDue cycle (load fresh, evaluate due, fire
// the escalation, save under optimistic concurrency) against the in-memory store.
func BenchmarkFireDue(b *testing.B) {
	ctx := context.Background()
	approve := workflow.MustNewTransition("approve", []workflow.Place{"submitted"}, []workflow.Place{"approved"})
	escalate := workflow.MustNewTransition("escalate", []workflow.Place{"submitted"}, []workflow.Place{"escalated"})
	escalate.SetTimeoutAfter(72 * time.Hour)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"submitted", "approved", "escalated"},
		[]workflow.Transition{*approve, *escalate},
	)
	if err != nil {
		b.Fatalf("NewDefinition: %v", err)
	}
	store := newMemStore()
	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	now := fireDueEpoch.Add(72 * time.Hour)

	b.ReportAllocs()
	for b.Loop() {
		b.StopTimer()
		_ = mgr.DeleteWorkflow(ctx, "wf")
		wf, err := workflow.NewWorkflow("wf", def, "submitted", workflow.WithClock(at(fireDueEpoch)))
		if err != nil {
			b.Fatalf("NewWorkflow: %v", err)
		}
		wf.SetManager(mgr)
		if err := mgr.SaveWorkflow(ctx, "wf", wf); err != nil {
			b.Fatalf("SaveWorkflow: %v", err)
		}
		b.StartTimer()

		if _, err := mgr.FireDue(ctx, "wf", def, now); err != nil {
			b.Fatalf("FireDue: %v", err)
		}
	}
}

func TestFireDue_UnsupportedStorageForListDue(t *testing.T) {
	// memStore is versioned but not a DueStorage: FireDue still works (it rides
	// Execute), but Manager.ListDue reports the missing capability.
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore(), workflow.WithoutRegistryCache())
	if _, err := mgr.ListDue(context.Background(), fireDueEpoch, 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ListDue on non-DueStorage backend = %v, want ErrUnsupported", err)
	}
}
