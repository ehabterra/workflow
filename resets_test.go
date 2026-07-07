package workflow_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

// rejectNet builds the canonical cancellation-region shape: an AND-split into
// two parallel branches where rejecting one branch resets the other.
func rejectNet(t *testing.T) *workflow.Definition {
	t.Helper()
	split := workflow.MustNewTransition("split", []workflow.Place{"start"}, []workflow.Place{"a", "b"})
	okA := workflow.MustNewTransition("ok_a", []workflow.Place{"a"}, []workflow.Place{"a_done"})
	rejectA := workflow.MustNewTransition("reject_a", []workflow.Place{"a"}, []workflow.Place{"rejected"})
	rejectA.SetResets("b", "b_done")
	okB := workflow.MustNewTransition("ok_b", []workflow.Place{"b"}, []workflow.Place{"b_done"})
	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "a", "b", "a_done", "b_done", "rejected"},
		[]workflow.Transition{*split, *okA, *rejectA, *okB},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

// TestResetArcClearsPlacesOnFire: firing a transition with reset arcs empties
// the reset places in the same atomic move.
func TestResetArcClearsPlacesOnFire(t *testing.T) {
	def := rejectNet(t)
	wf, err := workflow.NewWorkflow("wf", def, "start")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("split"); err != nil {
		t.Fatal(err)
	}
	if !wf.Marking().HasPlace("a") || !wf.Marking().HasPlace("b") {
		t.Fatalf("precondition: both branches marked, got %v", wf.CurrentPlaces())
	}

	if err := wf.ApplyTransition("reject_a"); err != nil {
		t.Fatalf("reject_a: %v", err)
	}
	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "rejected" {
		t.Fatalf("reset arc must clear the sibling branch, marking %v", places)
	}
	// The cancelled branch is dead: ok_b is no longer enabled.
	if err := wf.ApplyTransition("ok_b"); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("cancelled branch must not fire, got %v", err)
	}
}

// TestResetArcDoesNotAffectEnablement: reset places are not inputs — the
// transition fires whether or not they are marked.
func TestResetArcDoesNotAffectEnablement(t *testing.T) {
	def := rejectNet(t)
	wf, err := workflow.NewWorkflow("wf", def, "start")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("split"); err != nil {
		t.Fatal(err)
	}
	// Complete branch b first; reject_a resets b and b_done — b_done marked.
	if err := wf.ApplyTransition("ok_b"); err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("reject_a"); err != nil {
		t.Fatalf("reject with empty/occupied reset places must fire: %v", err)
	}
	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "rejected" {
		t.Fatalf("want only rejected, got %v", places)
	}
}

// TestResetArcOutputSurvives: a place that is both reset and an output keeps
// what THIS firing produces (reset clears before produce).
func TestResetArcOutputSurvives(t *testing.T) {
	// restart: consumes trigger, resets pool, and refills pool with one token.
	restart := workflow.MustNewTransition("restart", []workflow.Place{"trigger"}, []workflow.Place{"pool"})
	restart.SetResets("pool")
	def, err := workflow.NewDefinition(
		[]workflow.Place{"trigger", "pool"},
		[]workflow.Transition{*restart},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("wf", def, "trigger")
	if err != nil {
		t.Fatal(err)
	}
	// Fill the pool with stale colored tokens.
	for _, d := range []workflow.TokenData{{"n": 1}, {"n": 2}, {"n": 3}} {
		if _, err := wf.CreateToken("pool", d); err != nil {
			t.Fatal(err)
		}
	}
	if err := wf.ApplyTransition("restart"); err != nil {
		t.Fatal(err)
	}
	if got := wf.TokenCount("pool"); got != 1 {
		t.Fatalf("pool must hold exactly the produced token, got %d", got)
	}
}

// TestResetArcPerTokenFiring: reset arcs clear whole places in per-token
// firing too — including sibling tokens of the input place when it resets
// itself.
func TestResetArcPerTokenFiring(t *testing.T) {
	pick := workflow.MustNewTransition("pick", []workflow.Place{"queue"}, []workflow.Place{"chosen"})
	pick.SetResets("queue") // picking one discards the rest
	def, err := workflow.NewDefinition(
		[]workflow.Place{"queue", "chosen"},
		[]workflow.Transition{*pick},
	)
	if err != nil {
		t.Fatal(err)
	}
	m := workflow.NewMarking(nil)
	m.AddToken("queue", workflow.NewTokenWithID("t1", workflow.TokenData{"bid": 10}))
	m.AddToken("queue", workflow.NewTokenWithID("t2", workflow.TokenData{"bid": 20}))
	m.AddToken("queue", workflow.NewTokenWithID("t3", workflow.TokenData{"bid": 30}))
	wf, err := workflow.NewWorkflowFromMarking("wf", def, m)
	if err != nil {
		t.Fatal(err)
	}

	if err := wf.ApplyTransitionForToken(context.Background(), "pick", "t2"); err != nil {
		t.Fatal(err)
	}
	if got := wf.TokenCount("queue"); got != 0 {
		t.Fatalf("reset must discard the unchosen tokens, %d left", got)
	}
	chosen := wf.GetTokens("chosen")
	if len(chosen) != 1 || chosen[0].ID() != "t2" {
		t.Fatalf("want exactly t2 chosen, got %v", chosen)
	}
}

// TestResetArcKillsTimer: clearing a place kills the timer running on its
// token — the canonical zombie-escalation fix. The due index derives from the
// marking, so NextDue goes quiet.
func TestResetArcKillsTimer(t *testing.T) {
	def := rejectNet(t)
	// Put a 72h timer on branch b.
	def.Transition("ok_b").SetTimeoutAfter(72 * time.Hour)

	wf, err := workflow.NewWorkflow("wf", def, "start")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("split"); err != nil {
		t.Fatal(err)
	}
	if _, ok := wf.NextDue(); !ok {
		t.Fatal("precondition: a timer must be running on branch b")
	}

	if err := wf.ApplyTransition("reject_a"); err != nil {
		t.Fatal(err)
	}
	if due, ok := wf.NextDue(); ok {
		t.Fatalf("reset must kill the branch timer, still due at %v", due)
	}
	if fired := wf.Due(time.Now().Add(100 * time.Hour)); len(fired) != 0 {
		t.Fatalf("nothing may be due after the reset, got %v", fired)
	}
}

// TestResetArcValidation: NewDefinition rejects reset places that don't exist.
func TestResetArcValidation(t *testing.T) {
	tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})
	tr.SetResets("ghost")
	_, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want validation error naming the ghost place, got %v", err)
	}
}

// TestResetArcAccessors: SetResets deduplicates and Resets returns a copy.
func TestResetArcAccessors(t *testing.T) {
	tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})
	tr.SetResets("x", "y", "x")
	got := tr.Resets()
	if len(got) != 2 {
		t.Fatalf("want deduplicated resets, got %v", got)
	}
	got[0] = "mutated"
	if tr.Resets()[0] == "mutated" {
		t.Fatal("Resets must return a copy")
	}
	if workflow.MustNewTransition("t2", []workflow.Place{"a"}, []workflow.Place{"b"}).Resets() != nil {
		t.Fatal("no resets -> nil")
	}
}

// TestResetArcChangesFingerprint: resets are part of the net's structure.
func TestResetArcChangesFingerprint(t *testing.T) {
	build := func(withReset bool) *workflow.Definition {
		tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})
		if withReset {
			tr.SetResets("c")
		}
		def, err := workflow.NewDefinition([]workflow.Place{"a", "b", "c"}, []workflow.Transition{*tr})
		if err != nil {
			t.Fatal(err)
		}
		return def
	}
	if build(false).Fingerprint() == build(true).Fingerprint() {
		t.Fatal("adding a reset arc must change the fingerprint")
	}
	if build(true).Fingerprint() != build(true).Fingerprint() {
		t.Fatal("fingerprint must be stable")
	}
}

// TestResetArcConcurrentFiring: a reset firing racing a fire on the reset
// branch stays consistent — whatever interleaving wins, the final marking is
// coherent (either rejected-only, or b completed before the reset cleared
// b_done).
func TestResetArcConcurrentFiring(t *testing.T) {
	for range 50 {
		def := rejectNet(t)
		wf, err := workflow.NewWorkflow("wf", def, "start")
		if err != nil {
			t.Fatal(err)
		}
		if err := wf.ApplyTransition("split"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		for _, name := range []string{"reject_a", "ok_b"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = wf.ApplyTransition(name) // one may lose: ErrNotEnabled
			}()
		}
		wg.Wait()
		places := wf.CurrentPlaces()
		if len(places) != 1 || places[0] != "rejected" {
			t.Fatalf("reset must leave only rejected whatever the interleaving, got %v", places)
		}
	}
}
