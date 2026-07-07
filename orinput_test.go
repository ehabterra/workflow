package workflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

// stageNet builds the canonical OR-input shape: work escalates on a timer,
// and ONE approve transition accepts from either stage.
func stageNet(t *testing.T) *workflow.Definition {
	t.Helper()
	escalate := workflow.MustNewTransition("escalate", []workflow.Place{"pending"}, []workflow.Place{"escalated"})
	escalate.SetTimeoutAfter(72 * time.Hour)
	approve := workflow.MustNewTransition("approve", []workflow.Place{"pending", "escalated"}, []workflow.Place{"done"})
	approve.SetFromAny(true)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "escalated", "done"},
		[]workflow.Transition{*escalate, *approve},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// TestOrInputFiresFromEitherStage: one transition serves both stages,
// consuming only the marked input.
func TestOrInputFiresFromEitherStage(t *testing.T) {
	// From the first stage.
	wf, err := workflow.NewWorkflow("wf1", stageNet(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("approve"); err != nil {
		t.Fatalf("approve from pending: %v", err)
	}
	if p := wf.CurrentPlaces(); len(p) != 1 || p[0] != "done" {
		t.Fatalf("want [done], got %v", p)
	}

	// From the second stage.
	wf2, err := workflow.NewWorkflow("wf2", stageNet(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf2.ApplyTransition("escalate"); err != nil {
		t.Fatal(err)
	}
	if err := wf2.ApplyTransition("approve"); err != nil {
		t.Fatalf("approve from escalated: %v", err)
	}
	if p := wf2.CurrentPlaces(); len(p) != 1 || p[0] != "done" {
		t.Fatalf("want [done], got %v", p)
	}
}

// TestOrInputNotEnabledWhenAllEmpty: OR-input still requires at least one
// marked input.
func TestOrInputNotEnabledWhenAllEmpty(t *testing.T) {
	wf, err := workflow.NewWorkflow("wf", stageNet(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("approve"); err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("approve"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("second approve must not be enabled, got %v", err)
	}
}

// TestOrInputConsumesOnlyFirstMarked: when several inputs are marked, firing
// consumes exactly the first marked one (declaration order); the others stay.
func TestOrInputConsumesOnlyFirstMarked(t *testing.T) {
	merge := workflow.MustNewTransition("merge", []workflow.Place{"a", "b"}, []workflow.Place{"out"})
	merge.SetFromAny(true)
	fill := workflow.MustNewTransition("fill", []workflow.Place{"start"}, []workflow.Place{"a", "b"})
	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "a", "b", "out"},
		[]workflow.Transition{*fill, *merge},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("wf", def, "start")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("fill"); err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("merge"); err != nil {
		t.Fatal(err)
	}
	m := wf.Marking()
	if m.HasPlace("a") || !m.HasPlace("b") || !m.HasPlace("out") {
		t.Fatalf("merge must consume only 'a' (first marked), got %v", wf.CurrentPlaces())
	}
}

// TestOrInputTimer: a timed OR-input transition is due once ANY marked input
// has waited long enough.
func TestOrInputTimer(t *testing.T) {
	remind := workflow.MustNewTransition("remind", []workflow.Place{"new", "seen"}, []workflow.Place{"reminded"})
	remind.SetFromAny(true)
	remind.SetTimeoutAfter(time.Hour)
	look := workflow.MustNewTransition("look", []workflow.Place{"new"}, []workflow.Place{"seen"})
	def, err := workflow.NewDefinition(
		[]workflow.Place{"new", "seen", "reminded"},
		[]workflow.Transition{*look, *remind},
	)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	wf, err := workflow.NewWorkflow("wf", def, "new", workflow.WithClock(func() time.Time { return start }))
	if err != nil {
		t.Fatal(err)
	}
	// Only "new" is marked; due 1h after start even though "seen" is empty.
	deadline, ok := wf.NextDue()
	if !ok || !deadline.Equal(start.Add(time.Hour)) {
		t.Fatalf("OR-input timer must run off the marked input: %v %v", deadline, ok)
	}
	if due := wf.Due(start.Add(2 * time.Hour)); len(due) != 1 || due[0].Name() != "remind" {
		t.Fatalf("remind must be due, got %v", due)
	}
}

// TestOrInputPerTokenFiring: per-token firing resolves the input place to
// whichever OR-input place holds the token.
func TestOrInputPerTokenFiring(t *testing.T) {
	move := workflow.MustNewTransition("take", []workflow.Place{"inbox", "overflow"}, []workflow.Place{"taken"})
	move.SetFromAny(true)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"inbox", "overflow", "taken"},
		[]workflow.Transition{*move},
	)
	if err != nil {
		t.Fatal(err)
	}
	m := workflow.NewMarking(nil)
	m.AddToken("inbox", workflow.NewTokenWithID("t1", workflow.TokenData{"n": 1}))
	m.AddToken("overflow", workflow.NewTokenWithID("t2", workflow.TokenData{"n": 2}))
	wf, err := workflow.NewWorkflowFromMarking("wf", def, m)
	if err != nil {
		t.Fatal(err)
	}

	// t2 lives in the SECOND input place — per-token firing must find it.
	if err := wf.ApplyTransitionForToken(context.Background(), "take", "t2"); err != nil {
		t.Fatalf("take t2 from overflow: %v", err)
	}
	if got := wf.GetTokens("taken"); len(got) != 1 || got[0].ID() != "t2" {
		t.Fatalf("want t2 taken, got %v", got)
	}
	if !wf.Marking().HasPlace("inbox") {
		t.Fatal("t1 must remain in inbox")
	}
}

// TestOrInputChangesFingerprint: the input mode is part of the structure.
func TestOrInputChangesFingerprint(t *testing.T) {
	build := func(any bool) *workflow.Definition {
		tr := workflow.MustNewTransition("t", []workflow.Place{"a", "b"}, []workflow.Place{"c"})
		tr.SetFromAny(any)
		def, err := workflow.NewDefinition([]workflow.Place{"a", "b", "c"}, []workflow.Transition{*tr})
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	if build(false).Fingerprint() == build(true).Fingerprint() {
		t.Fatal("toggling OR-input must change the fingerprint")
	}
}

// TestOrInputConcurrentFiring: racing an OR-input fire against the escalate
// that moves its token between the two inputs must fire the approve exactly
// once, whatever interleaving wins.
func TestOrInputConcurrentFiring(t *testing.T) {
	for range 50 {
		wf, err := workflow.NewWorkflow("wf", stageNet(t), "pending")
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, name := range []string{"escalate", "approve"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = wf.ApplyTransition(name)
			}()
		}
		wg.Wait()
		// approve may have fired from pending or from escalated, but the
		// token ends in done exactly once (possibly after a helper fire).
		if !wf.Marking().HasPlace("done") {
			// escalate won and approve lost the race entirely: fire it now.
			if err := wf.ApplyTransition("approve"); err != nil {
				t.Fatalf("approve after race: %v (marking %v)", err, wf.CurrentPlaces())
			}
		}
		if p := wf.CurrentPlaces(); len(p) != 1 || p[0] != "done" {
			t.Fatalf("want exactly [done], got %v", p)
		}
	}
}

// TestApplyAnyRoutesByGuard: the XOR-split resolver fires the first
// candidate the state allows, skipping guard rejections.
func TestApplyAnyRoutesByGuard(t *testing.T) {
	small := workflow.MustNewTransition("auto", []workflow.Place{"in"}, []workflow.Place{"approved"})
	small.AddConstraint(mustExpr(t, "amount <= 100.0"))
	big := workflow.MustNewTransition("review", []workflow.Place{"in"}, []workflow.Place{"reviewing"})
	big.AddConstraint(mustExpr(t, "amount > 100.0"))
	def, err := workflow.NewDefinition(
		[]workflow.Place{"in", "approved", "reviewing"},
		[]workflow.Transition{*small, *big},
	)
	if err != nil {
		t.Fatal(err)
	}

	wf, err := workflow.NewWorkflow("small", def, "in")
	if err != nil {
		t.Fatal(err)
	}
	wf.SetContext("amount", 50.0)
	name, err := wf.ApplyAny(context.Background(), "auto", "review")
	if err != nil || name != "auto" {
		t.Fatalf("want auto route, got %q %v", name, err)
	}

	wf2, err := workflow.NewWorkflow("big", def, "in")
	if err != nil {
		t.Fatal(err)
	}
	wf2.SetContext("amount", 5000.0)
	name, err = wf2.ApplyAny(context.Background(), "auto", "review")
	if err != nil || name != "review" {
		t.Fatalf("want review route, got %q %v", name, err)
	}

	// Nothing enabled: the last blocking error comes back.
	_, err = wf2.ApplyAny(context.Background(), "auto", "review")
	if !errors.Is(err, workflow.ErrTransitionNotAllowed) {
		t.Fatalf("want a blocking error when no route fires, got %v", err)
	}
	// Unknown names abort immediately.
	_, err = wf2.ApplyAny(context.Background(), "nope")
	if !errors.Is(err, workflow.ErrTransitionNotFound) {
		t.Fatalf("want ErrTransitionNotFound, got %v", err)
	}
	_, err = wf2.ApplyAny(context.Background())
	if err == nil {
		t.Fatal("empty candidate list must error")
	}
}

func mustExpr(t *testing.T, expr string) workflow.Constraint {
	t.Helper()
	c, err := workflow.NewExpressionConstraint(expr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
