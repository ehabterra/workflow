// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

// observerNet: in --go--> out, with an optional guard expression on go.
func observerNet(t *testing.T, guard string) *workflow.Definition {
	t.Helper()
	tr := workflow.MustNewTransition("go", []workflow.Place{"in"}, []workflow.Place{"out"})
	if guard != "" {
		gc, err := workflow.NewExpressionConstraint(guard)
		if err != nil {
			t.Fatalf("compile guard: %v", err)
		}
		tr.AddConstraint(gc)
	}
	def, err := workflow.NewDefinition([]workflow.Place{"in", "out"}, []workflow.Transition{*tr})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// TestObserver_SeesEventsAndCannotBlock: an observer receives before/after
// events, and a panicking observer neither aborts the firing nor escapes.
func TestObserver_SeesEventsAndCannotBlock(t *testing.T) {
	def := observerNet(t, "")
	wf, err := workflow.NewWorkflow("wf", def, "in")
	if err != nil {
		t.Fatal(err)
	}

	var seen []workflow.EventType
	wf.AddObserver(workflow.EventBeforeTransition, func(e workflow.Event) {
		seen = append(seen, e.Type())
		panic("instrumentation bug") // must be recovered, never abort the firing
	})
	wf.AddObserver(workflow.EventAfterTransition, func(e workflow.Event) {
		seen = append(seen, e.Type())
		if e.Transition() == nil || e.Transition().Name() != "go" {
			t.Errorf("after-event transition = %v, want go", e.Transition())
		}
	})

	if err := wf.ApplyTransition("go"); err != nil {
		t.Fatalf("a panicking observer must not fail the firing: %v", err)
	}
	if len(seen) != 2 || seen[0] != workflow.EventBeforeTransition || seen[1] != workflow.EventAfterTransition {
		t.Fatalf("observed events = %v, want [before after]", seen)
	}
	if !wf.Marking().HasPlace("out") {
		t.Fatalf("marking = %v, want [out]", wf.CurrentPlaces())
	}
}

// TestObserver_GuardRejectedEvent: guard rejections — by expression
// constraint AND by a blocking guard listener — emit the observability-only
// EventGuardRejected to observers.
func TestObserver_GuardRejectedEvent(t *testing.T) {
	t.Run("expression constraint", func(t *testing.T) {
		def := observerNet(t, "getContext('allowed', false) == true")
		wf, err := workflow.NewWorkflow("wf", def, "in")
		if err != nil {
			t.Fatal(err)
		}
		rejected := 0
		wf.AddObserver(workflow.EventGuardRejected, func(e workflow.Event) {
			rejected++
			if e.Transition().Name() != "go" {
				t.Errorf("rejected transition = %q, want go", e.Transition().Name())
			}
		})
		if err := wf.ApplyTransition("go"); !errors.Is(err, workflow.ErrGuardRejected) {
			t.Fatalf("ApplyTransition = %v, want ErrGuardRejected", err)
		}
		if rejected != 1 {
			t.Fatalf("EventGuardRejected observed %d times, want 1", rejected)
		}
	})

	t.Run("blocking guard listener", func(t *testing.T) {
		def := observerNet(t, "")
		wf, err := workflow.NewWorkflow("wf", def, "in")
		if err != nil {
			t.Fatal(err)
		}
		wf.AddGuardEventListener(func(e *workflow.GuardEvent) error {
			e.SetBlocking(true)
			return nil
		})
		rejected := 0
		wf.AddObserver(workflow.EventGuardRejected, func(e workflow.Event) { rejected++ })
		if err := wf.ApplyTransition("go"); !errors.Is(err, workflow.ErrGuardRejected) {
			t.Fatalf("ApplyTransition = %v, want ErrGuardRejected", err)
		}
		if rejected != 1 {
			t.Fatalf("EventGuardRejected observed %d times, want 1", rejected)
		}
	})

	t.Run("per-token firing", func(t *testing.T) {
		def := observerNet(t, "token.amount <= 100.0")
		m := workflow.NewMarking(nil)
		m.AddToken("in", workflow.NewTokenWithID("t1", workflow.TokenData{"amount": 500.0}))
		wf, err := workflow.NewWorkflowFromMarking("wf", def, m)
		if err != nil {
			t.Fatal(err)
		}
		rejected := 0
		wf.AddObserver(workflow.EventGuardRejected, func(e workflow.Event) {
			rejected++
			if len(e.Tokens()) != 1 || e.Tokens()[0].ID() != "t1" {
				t.Errorf("rejected tokens = %v, want [t1]", e.Tokens())
			}
		})
		if err := wf.ApplyTransitionForToken(context.Background(), "go", "t1"); !errors.Is(err, workflow.ErrGuardRejected) {
			t.Fatalf("ApplyTransitionForToken = %v, want ErrGuardRejected", err)
		}
		if rejected != 1 {
			t.Fatalf("EventGuardRejected observed %d times, want 1", rejected)
		}
	})
}

// TestObserver_GuardRejectedNeverReachesErrorListeners: an error-returning
// EventListener registered for EventGuardRejected is never invoked — the
// event is observability-only, so instrumentation can't add a failure mode.
func TestObserver_GuardRejectedNeverReachesErrorListeners(t *testing.T) {
	def := observerNet(t, "getContext('allowed', false) == true")
	wf, err := workflow.NewWorkflow("wf", def, "in")
	if err != nil {
		t.Fatal(err)
	}
	wf.AddEventListener(workflow.EventGuardRejected, func(e workflow.Event) error {
		t.Error("error-returning listener must not receive EventGuardRejected")
		return errors.New("boom")
	})
	if err := wf.ApplyTransition("go"); !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("ApplyTransition = %v, want ErrGuardRejected (not the listener's boom)", err)
	}
}

// TestObserver_ManagerLevelAndRemoval: manager observers see every managed
// instance's firings, and RemoveListener stops them.
func TestObserver_ManagerLevelAndRemoval(t *testing.T) {
	ctx := context.Background()
	def := observerNet(t, "")
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore())
	if _, err := mgr.CreateWorkflow(ctx, "wf", def, "in"); err != nil {
		t.Fatal(err)
	}

	fired := 0
	handle := mgr.AddObserver(workflow.EventAfterTransition, func(e workflow.Event) { fired++ })

	if err := mgr.Execute(ctx, "wf", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransition("go")
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if fired != 1 {
		t.Fatalf("manager observer saw %d firings, want 1", fired)
	}

	mgr.RemoveListener(handle)
	if _, err := mgr.CreateWorkflow(ctx, "wf2", def, "in"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Execute(ctx, "wf2", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransition("go")
	}); err != nil {
		t.Fatalf("Execute(wf2): %v", err)
	}
	if fired != 1 {
		t.Fatalf("removed observer still firing: %d", fired)
	}
}
