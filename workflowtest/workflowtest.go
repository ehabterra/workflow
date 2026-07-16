// Package workflowtest provides test helpers for code built on the workflow
// library: marking assertions, a transition path runner, a guard harness
// that table-tests guard expressions without storage or a Manager, and a
// deterministic clock for timer tests — so host-application tests never
// sleep, poll, or hand-roll marking comparisons.
//
// Everything here uses only the library's public API; it is the M5.2
// counterpart to the storagetest conformance kit.
package workflowtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

// --- Marking assertions ---

// AssertMarking fails t unless the workflow's marked places are EXACTLY want
// (order-independent). Use it to pin a full state; use AssertHas for subset
// checks.
func AssertMarking(t testing.TB, wf *workflow.Workflow, want ...workflow.Place) {
	t.Helper()
	got := wf.Marking().Places()
	wantSorted := append([]workflow.Place(nil), want...)
	slices.Sort(wantSorted)
	// got is already sorted by Marking().Places(); sort defensively anyway.
	gotSorted := append([]workflow.Place(nil), got...)
	slices.Sort(gotSorted)
	if !slices.Equal(gotSorted, wantSorted) {
		t.Fatalf("marking = %v, want exactly %v", got, wantSorted)
	}
}

// AssertHas fails t unless every given place is marked.
func AssertHas(t testing.TB, wf *workflow.Workflow, places ...workflow.Place) {
	t.Helper()
	for _, p := range places {
		if !wf.Marking().HasPlace(p) {
			t.Fatalf("place %q is not marked (marking %v)", p, wf.CurrentPlaces())
		}
	}
}

// AssertNotHas fails t if any of the given places is marked.
func AssertNotHas(t testing.TB, wf *workflow.Workflow, places ...workflow.Place) {
	t.Helper()
	for _, p := range places {
		if wf.Marking().HasPlace(p) {
			t.Fatalf("place %q is marked (marking %v), want unmarked", p, wf.CurrentPlaces())
		}
	}
}

// --- Path runner ---

// Apply fires the named transitions in order — the path runner: "apply
// submit → legal_approve → finance_approve, then assert approved". It fails
// t on the first transition that errors, naming the step, the error, and the
// marking it was attempted from.
func Apply(t testing.TB, wf *workflow.Workflow, transitions ...string) {
	t.Helper()
	ApplyContext(t, context.Background(), wf, transitions...)
}

// ApplyContext is Apply with an explicit context (deadlines, tracing).
func ApplyContext(t testing.TB, ctx context.Context, wf *workflow.Workflow, transitions ...string) {
	t.Helper()
	for i, name := range transitions {
		before := wf.CurrentPlaces()
		if err := wf.ApplyTransitionWithContext(ctx, name); err != nil {
			t.Fatalf("path step %d/%d %q from %v: %v", i+1, len(transitions), name, before, err)
		}
	}
}

// --- Guard harness ---

// A GuardCase is one row of a guard table test: the workflow context and the
// colored tokens the guard should see, and whether it must allow the firing.
type GuardCase struct {
	Name    string
	Context map[string]any
	Tokens  []workflow.Token
	Allow   bool
}

// AssertGuard table-tests the named transition's guards against each case,
// with no storage and no Manager: for every case it builds a throwaway
// instance from the definition with the transition's input places marked
// (tokens placed on the first input), sets the context, and attempts the
// firing. A case with Allow: true must fire; a case with Allow: false must
// be refused with ErrGuardRejected. Any other outcome fails t with the case
// name.
func AssertGuard(t testing.TB, def *workflow.Definition, transition string, cases ...GuardCase) {
	t.Helper()
	tr := def.Transition(transition)
	if tr == nil {
		t.Fatalf("transition %q is not in the definition", transition)
	}
	for _, c := range cases {
		err := fireScratch(def, tr, c.Context, c.Tokens)
		switch {
		case c.Allow && err != nil:
			t.Fatalf("guard case %q: want allowed, got %v", c.Name, err)
		case !c.Allow && !errors.Is(err, workflow.ErrGuardRejected):
			t.Fatalf("guard case %q: want ErrGuardRejected, got %v", c.Name, err)
		}
	}
}

// AssertGuardAllows asserts that the transition's guards allow a firing with
// the given context (and optional tokens on its first input place).
func AssertGuardAllows(t testing.TB, def *workflow.Definition, transition string, ctxData map[string]any, tokens ...workflow.Token) {
	t.Helper()
	AssertGuard(t, def, transition, GuardCase{Name: "allows", Context: ctxData, Tokens: tokens, Allow: true})
}

// AssertGuardRejects asserts that the transition's guards refuse a firing
// with the given context (and optional tokens on its first input place).
func AssertGuardRejects(t testing.TB, def *workflow.Definition, transition string, ctxData map[string]any, tokens ...workflow.Token) {
	t.Helper()
	AssertGuard(t, def, transition, GuardCase{Name: "rejects", Context: ctxData, Tokens: tokens, Allow: false})
}

// fireScratch attempts one firing of tr on a fresh throwaway instance whose
// input places are marked, returning the firing's error.
func fireScratch(def *workflow.Definition, tr *workflow.Transition, ctxData map[string]any, tokens []workflow.Token) error {
	m := workflow.NewMarking(nil)
	from := tr.From()
	for i, p := range from {
		if i == 0 && len(tokens) > 0 {
			for _, tok := range tokens {
				m.AddToken(p, tok)
			}
			continue
		}
		if err := m.AddPlace(p); err != nil {
			return fmt.Errorf("marking input %q: %w", p, err)
		}
	}
	wf, err := workflow.NewWorkflowFromMarking("workflowtest-guard", def, m)
	if err != nil {
		return fmt.Errorf("building scratch instance: %w", err)
	}
	for k, v := range ctxData {
		wf.SetContext(k, v)
	}
	return wf.ApplyTransitionWithContext(context.Background(), tr.Name())
}

// --- Timers ---

// Clock is a deterministic, manually advanced clock for timer tests: pass
// Now to workflow.WithClock and to Due/ListDue/FireDue, and Advance it
// instead of sleeping.
//
//	clk := workflowtest.NewClock(t0)
//	wf, _ := workflow.NewWorkflow("wf", def, "submitted", workflow.WithClock(clk.Now))
//	clk.Advance(72 * time.Hour)
//	fired, _ := mgr.FireDue(ctx, "wf", def, clk.Now())
//
// It is safe for concurrent use.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a Clock frozen at start.
func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

// Now returns the clock's current instant. Its method value (clk.Now) is a
// func() time.Time, ready for workflow.WithClock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d and returns the new instant.
func (c *Clock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// Set jumps the clock to t.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// AssertDue fails t unless the transitions due at now are exactly want
// (order-independent; empty want asserts nothing is due).
func AssertDue(t testing.TB, wf *workflow.Workflow, now time.Time, want ...string) {
	t.Helper()
	due := wf.Due(now)
	got := make([]string, len(due))
	for i := range due {
		got[i] = due[i].Name()
	}
	slices.Sort(got)
	wantSorted := append([]string(nil), want...)
	slices.Sort(wantSorted)
	if !slices.Equal(got, wantSorted) {
		t.Fatalf("due at %v = %v, want %v", now, got, wantSorted)
	}
}
