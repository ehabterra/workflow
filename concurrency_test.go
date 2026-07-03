package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/ehabterra/workflow"
)

// abDefinition returns a minimal a --go--> b definition.
func abDefinition(t *testing.T) *workflow.Definition {
	t.Helper()
	tr, err := workflow.NewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
	if err != nil {
		t.Fatalf("NewTransition: %v", err)
	}
	def, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// A transition must fire exactly once even when many goroutines race to apply
// it: the enablement re-check under the write lock rejects the losers.
func TestConcurrentApplyTransition_FiresOnce(t *testing.T) {
	def := abDefinition(t)
	wf, err := workflow.NewWorkflow("wf", def, "a")
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}

	const racers = 32
	var wg sync.WaitGroup
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = wf.ApplyTransition("go")
		}(i)
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, workflow.ErrTransitionNotAllowed):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("transition fired %d times, want exactly 1", succeeded)
	}
	if wf.Marking().HasPlace("a") {
		t.Fatal("place a still marked after firing")
	}
	if got := wf.TokenCount("b"); got != 1 {
		t.Fatalf("TokenCount(b) = %d, want 1 (no duplicate produce)", got)
	}
}

// With a colored token, a lost race must not fall back to producing a phantom
// uncolored token at the output place.
func TestConcurrentApplyTransition_NoPhantomToken(t *testing.T) {
	def := abDefinition(t)
	wf, err := workflow.NewWorkflow("wf", def, "a")
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	wf.ClearPlace("a")
	tok, err := wf.CreateToken("a", workflow.TokenData{"order": "A-1"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}

	const racers = 16
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = wf.ApplyTransition("go")
		}()
	}
	wg.Wait()

	if got := wf.TokenCount("b"); got != 1 {
		t.Fatalf("TokenCount(b) = %d, want 1 (phantom token produced by racing fire)", got)
	}
	toks := wf.GetTokens("b")
	if len(toks) != 1 || toks[0].ID() != tok.ID() {
		t.Fatalf("token at b = %+v, want the original colored token %s", toks, tok.ID())
	}
}

// marshalingStorage encodes what it is handed, like a real SQL backend, so the
// race detector sees any concurrent mutation of live state during a save.
type marshalingStorage struct{}

func (marshalingStorage) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, error) {
	return nil, nil, workflow.ErrWorkflowNotFound
}

func (marshalingStorage) SaveState(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any) error {
	if _, err := json.Marshal(marking); err != nil {
		return err
	}
	_, err := json.Marshal(ctxData)
	return err
}

func (marshalingStorage) DeleteState(ctx context.Context, id string) error { return nil }

// SaveWorkflow must snapshot marking and context under the workflow lock;
// otherwise this test races SetContext and transition firing against the
// storage layer's marshaling.
func TestManagerSaveWorkflow_ConcurrentMutation(t *testing.T) {
	tr1, _ := workflow.NewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
	tr2, _ := workflow.NewTransition("back", []workflow.Place{"b"}, []workflow.Place{"a"})
	def, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr1, *tr2})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	mgr := workflow.NewManager(workflow.NewRegistry(), marshalingStorage{})
	ctx := context.Background()
	wf, err := mgr.CreateWorkflow(ctx, "wf", def, "a")
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			wf.SetContext("counter", i)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = wf.ApplyTransition("go")
			_ = wf.ApplyTransition("back")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			if err := mgr.SaveWorkflow(ctx, "wf", wf); err != nil {
				t.Errorf("SaveWorkflow: %v", err)
				return
			}
		}
	}()
	wg.Wait()
}

// Listeners may be added and removed on the definition, manager, and instance
// while transitions fire on another goroutine.
func TestListeners_ConcurrentAddRemoveWhileFiring(t *testing.T) {
	tr1, _ := workflow.NewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
	tr2, _ := workflow.NewTransition("back", []workflow.Place{"b"}, []workflow.Place{"a"})
	def, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr1, *tr2})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	mgr := workflow.NewManager(workflow.NewRegistry(), marshalingStorage{})
	wf, err := mgr.CreateWorkflow(context.Background(), "wf", def, "a")
	if err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	noop := func(workflow.Event) error { return nil }

	const iterations = 200
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			hd := def.AddEventListener(workflow.EventAfterTransition, noop)
			hm := mgr.AddEventListener(workflow.EventBeforeTransition, noop)
			hw := wf.AddEventListener(workflow.EventAfterTransition, noop)
			def.RemoveListener(hd)
			mgr.RemoveListener(hm)
			wf.RemoveListener(hw)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = wf.ApplyTransition("go")
			_ = wf.ApplyTransition("back")
		}
	}()
	wg.Wait()
}

// Removing a listener of one event type must not corrupt the handle indices of
// another event type's listeners (regression: the handle map was shared across
// event types, so a removal shifted every later index regardless of type).
func TestRemoveListener_CrossEventTypeIndices(t *testing.T) {
	def := abDefinition(t)

	var calls []string
	record := func(name string) workflow.EventListener {
		return func(workflow.Event) error {
			calls = append(calls, name)
			return nil
		}
	}

	h1 := def.AddEventListener(workflow.EventBeforeTransition, record("L1"))
	h2 := def.AddEventListener(workflow.EventBeforeTransition, record("L2"))
	h3 := def.AddEventListener(workflow.EventAfterTransition, record("L3"))
	_ = h1

	// Removing the after-listener must not shift the before-listeners' indices…
	def.RemoveListener(h3)
	// …so removing L2 by handle must remove L2, not L1.
	def.RemoveListener(h2)

	wf, err := workflow.NewWorkflow("wf", def, "a")
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	if err := wf.ApplyTransition("go"); err != nil {
		t.Fatalf("ApplyTransition: %v", err)
	}

	if len(calls) != 1 || calls[0] != "L1" {
		t.Fatalf("fired listeners = %v, want [L1] (wrong listener removed)", calls)
	}
	if got := def.ListenerCount(workflow.EventBeforeTransition); got != 1 {
		t.Fatalf("before-listener count = %d, want 1", got)
	}
	if got := def.ListenerCount(workflow.EventAfterTransition); got != 0 {
		t.Fatalf("after-listener count = %d, want 0", got)
	}
}
