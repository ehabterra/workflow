package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ehabterra/workflow"
)

// memStore is a faithful in-memory VersionedStorage + ListableStorage used to
// exercise Manager behavior deterministically. It serializes marking and context
// through JSON exactly like the SQL backends, so it observes the same value-type
// round-tripping and never aliases the workflow's live state.
type memStore struct {
	mu   sync.Mutex
	rows map[string]memRow
}

type memRow struct {
	state   []byte
	context []byte
	version int64
}

func newMemStore() *memStore { return &memStore{rows: make(map[string]memRow)} }

func (s *memStore) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, error) {
	m, c, _, err := s.LoadVersionedState(ctx, id)
	return m, c, err
}

func (s *memStore) LoadVersionedState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if !ok {
		return nil, nil, 0, fmt.Errorf("%w: %s", workflow.ErrWorkflowNotFound, id)
	}
	m, err := workflow.UnmarshalMarkingJSON(row.state)
	if err != nil {
		return nil, nil, 0, err
	}
	c := make(map[string]any)
	if len(row.context) > 0 {
		if err := json.Unmarshal(row.context, &c); err != nil {
			return nil, nil, 0, err
		}
	}
	return m, c, row.version, nil
}

func (s *memStore) SaveState(ctx context.Context, id string, m workflow.Marking, c map[string]any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.putLocked(id, m, c, s.rows[id].version+1)
}

func (s *memStore) SaveVersionedState(ctx context.Context, id string, m workflow.Marking, c map[string]any, expected int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[id]
	if expected <= 0 {
		if ok {
			return 0, fmt.Errorf("%w: %s already exists", workflow.ErrConflict, id)
		}
		if err := s.putLocked(id, m, c, 1); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if !ok || row.version != expected {
		return 0, fmt.Errorf("%w: %s (expected %d)", workflow.ErrConflict, id, expected)
	}
	newV := expected + 1
	if err := s.putLocked(id, m, c, newV); err != nil {
		return 0, err
	}
	return newV, nil
}

func (s *memStore) putLocked(id string, m workflow.Marking, c map[string]any, version int64) error {
	state, err := json.Marshal(m)
	if err != nil {
		return err
	}
	cbytes, err := json.Marshal(c)
	if err != nil {
		return err
	}
	s.rows[id] = memRow{state: state, context: cbytes, version: version}
	return nil
}

func (s *memStore) DeleteState(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	return nil
}

// abDef is a small a --go--> b definition; abDefinition lives in concurrency_test.go.
func abDef(t *testing.T) *workflow.Definition { return abDefinition(t) }

func TestDefinitionFingerprint_OrderIndependentAndGuardSensitive(t *testing.T) {
	build := func(order int, guard string) *workflow.Definition {
		t1 := workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
		if guard != "" {
			t1.SetMetadata("guard", guard)
		}
		t2 := workflow.MustNewTransition("back", []workflow.Place{"b"}, []workflow.Place{"a"})
		places := []workflow.Place{"a", "b"}
		trs := []workflow.Transition{*t1, *t2}
		if order == 1 { // same net, different declaration order
			places = []workflow.Place{"b", "a"}
			trs = []workflow.Transition{*t2, *t1}
		}
		def, err := workflow.NewDefinition(places, trs)
		if err != nil {
			t.Fatalf("NewDefinition: %v", err)
		}
		return def
	}

	if a, b := build(0, "").Fingerprint(), build(1, "").Fingerprint(); a != b {
		t.Errorf("fingerprint is order-dependent: %s != %s", a, b)
	}
	if a, b := build(0, "").Fingerprint(), build(0, "token.amount > 10").Fingerprint(); a == b {
		t.Error("fingerprint ignored a guard change")
	}
}

func TestManager_DefinitionFingerprint_RoundTripAndMismatch(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	def := abDef(t)

	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	if _, err := mgr.CreateWorkflow(ctx, "wf", def, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	// Same definition loads fine, and the fingerprint never leaks into context.
	wf, err := mgr.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("LoadWorkflow(same def): %v", err)
	}
	for k := range wf.AllContext() {
		if k == "__workflow_def_fingerprint" {
			t.Fatal("definition fingerprint leaked into workflow context")
		}
	}

	// A changed definition (extra place + transition) is rejected...
	changed, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b", "c"},
		[]workflow.Transition{
			*workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"}),
			*workflow.MustNewTransition("more", []workflow.Place{"b"}, []workflow.Place{"c"}),
		},
	)
	if err != nil {
		t.Fatalf("NewDefinition(changed): %v", err)
	}
	if _, err := mgr.LoadWorkflow(ctx, "wf", changed); !errors.Is(err, workflow.ErrDefinitionMismatch) {
		t.Fatalf("LoadWorkflow(changed def) err = %v, want ErrDefinitionMismatch", err)
	}

	// ...unless a migration handler approves it.
	called := false
	migrating := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache(),
		workflow.WithDefinitionMigration(func(_ context.Context, id, stored, current string) error {
			called = true
			if stored == "" || current == "" || stored == current {
				t.Errorf("unexpected fingerprints stored=%q current=%q", stored, current)
			}
			return nil
		}))
	if _, err := migrating.LoadWorkflow(ctx, "wf", changed); err != nil {
		t.Fatalf("LoadWorkflow with migration handler: %v", err)
	}
	if !called {
		t.Error("migration handler was not consulted on mismatch")
	}
}

func TestManager_LoadValidatesAllPlaces(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	def := abDef(t)

	// Persist a marking that references a place not in the definition. Bypass the
	// Manager (which would reject it on create) and write straight to storage.
	stray := workflow.NewMarking([]workflow.Place{"a", "ghost"})
	if _, err := store.SaveVersionedState(ctx, "wf", stray, map[string]any{}, 0); err != nil {
		t.Fatalf("seed SaveVersionedState: %v", err)
	}

	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	if _, err := mgr.LoadWorkflow(ctx, "wf", def); !errors.Is(err, workflow.ErrDefinitionMismatch) {
		t.Fatalf("LoadWorkflow with stray place err = %v, want ErrDefinitionMismatch", err)
	}
}

// conflictInjector fails the first n SaveVersionedState calls with ErrConflict
// to drive Execute's retry loop deterministically.
type conflictInjector struct {
	*memStore
	remaining int32
}

func (c *conflictInjector) SaveVersionedState(ctx context.Context, id string, m workflow.Marking, cd map[string]any, expected int64) (int64, error) {
	if atomic.AddInt32(&c.remaining, -1) >= 0 {
		return 0, fmt.Errorf("%w: injected", workflow.ErrConflict)
	}
	return c.memStore.SaveVersionedState(ctx, id, m, cd, expected)
}

func TestManager_Execute_RetriesOnConflict(t *testing.T) {
	store := &conflictInjector{memStore: newMemStore()}
	ctx := context.Background()
	def := abDef(t)

	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	if _, err := mgr.CreateWorkflow(ctx, "wf", def, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	// Fail the next two saves; Execute should reload and retry until the third
	// succeeds, invoking fn once per attempt.
	atomic.StoreInt32(&store.remaining, 2)
	runs := 0
	err := mgr.Execute(ctx, "wf", def, func(wf *workflow.Workflow) error {
		runs++
		return wf.ApplyTransition("go")
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if runs != 3 {
		t.Fatalf("fn ran %d times, want 3 (2 conflicts + success)", runs)
	}
}

// Concurrent Executes that each increment a context counter must not lose
// updates: optimistic concurrency + retry serializes them.
func TestManager_Execute_NoLostUpdates(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	def := abDef(t)

	mgr := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	if _, err := mgr.CreateWorkflow(ctx, "wf", def, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := mgr.Execute(ctx, "wf", def, func(wf *workflow.Workflow) error {
				n := 0.0
				if v, ok := wf.Context("count"); ok {
					n, _ = v.(float64) // JSON round-trips numbers as float64
				}
				wf.SetContext("count", n+1)
				return nil
			})
			if err != nil {
				t.Errorf("Execute: %v", err)
			}
		}()
	}
	wg.Wait()

	wf, err := mgr.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	got, _ := wf.Context("count")
	if got != float64(workers) {
		t.Fatalf("final count = %v, want %d (lost updates)", got, workers)
	}
}

func TestManager_WithoutRegistryCache_FreshLoads(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	def := abDef(t)

	// Default manager caches: two gets return the same instance.
	cached := workflow.NewManager(workflow.NewRegistry(), store)
	if _, err := cached.CreateWorkflow(ctx, "wf", def, "a"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}
	a, _ := cached.GetWorkflow(ctx, "wf", def)
	b, _ := cached.GetWorkflow(ctx, "wf", def)
	if a != b {
		t.Fatal("cached manager returned different instances for the same id")
	}

	// Fresh-load manager returns a new instance each time and reflects saves.
	fresh := workflow.NewManager(workflow.NewRegistry(), store, workflow.WithoutRegistryCache())
	c, _ := fresh.GetWorkflow(ctx, "wf", def)
	d, _ := fresh.GetWorkflow(ctx, "wf", def)
	if c == d {
		t.Fatal("fresh-load manager returned the same cached instance")
	}
	if err := c.ApplyTransition("go"); err != nil {
		t.Fatalf("ApplyTransition: %v", err)
	}
	if err := fresh.SaveWorkflow(ctx, "wf", c); err != nil {
		t.Fatalf("SaveWorkflow: %v", err)
	}
	reloaded, _ := fresh.GetWorkflow(ctx, "wf", def)
	if !reloaded.Marking().HasPlace("b") {
		t.Fatalf("fresh reload did not see the saved transition: %v", reloaded.CurrentPlaces())
	}
}

func TestManager_ListWorkflowIDs(t *testing.T) {
	store := newMemStore()
	ctx := context.Background()
	def := abDef(t)
	mgr := workflow.NewManager(workflow.NewRegistry(), store)

	// memStore does not implement ListableStorage, so this surfaces the error.
	if _, err := mgr.ListWorkflowIDs(ctx, workflow.ListOptions{}); !errors.Is(err, workflow.ErrInvalidWorkflow) {
		t.Fatalf("ListWorkflowIDs on non-listable store err = %v, want ErrInvalidWorkflow", err)
	}
	_ = def
}
