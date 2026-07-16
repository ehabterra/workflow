package workflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

func timedWF(t *testing.T) *workflow.Workflow {
	t.Helper()
	tr := workflow.MustNewTransition("escalate", []workflow.Place{"waiting"}, []workflow.Place{"escalated"})
	tr.SetTimeoutAfter(time.Hour)
	def, err := workflow.NewDefinition([]workflow.Place{"waiting", "escalated"}, []workflow.Transition{*tr})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("timed", def, "waiting")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestTimersDueAndNextDue(t *testing.T) {
	wf := timedWF(t)
	// The presence token in "waiting" was stamped at creation (timed definition).
	now := time.Now()

	// Nothing is due yet.
	if due := wf.Due(now); len(due) != 0 {
		t.Fatalf("Due(now) = %v, want none", due)
	}
	// There is a next-due time in the future.
	if nd, ok := wf.NextDue(); !ok || !nd.After(now) {
		t.Fatalf("NextDue = (%v,%v), want a future time", nd, ok)
	}
	// After the timeout window, the transition is due.
	if due := wf.Due(now.Add(2 * time.Hour)); len(due) != 1 || due[0].Name() != "escalate" {
		t.Fatalf("Due(later) = %v, want [escalate]", due)
	}
}

func TestNextDueNoTimers(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("untimed", def, "a")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := wf.NextDue(); ok {
		t.Fatal("a workflow with no timed transitions has no next-due time")
	}
}

func TestCreateTokenInvalidPlace(t *testing.T) {
	wf := timedWF(t)
	if _, err := wf.CreateToken("nowhere", workflow.TokenData{"x": 1}); !errors.Is(err, workflow.ErrInvalidPlace) {
		t.Fatalf("CreateToken(bad place) = %v, want ErrInvalidPlace", err)
	}
	if _, err := wf.CreateTokens("nowhere", []workflow.TokenData{{"x": 1}}); !errors.Is(err, workflow.ErrInvalidPlace) {
		t.Fatalf("CreateTokens(bad place) = %v, want ErrInvalidPlace", err)
	}
	// CreateTokens on a timed definition stamps every token's entry time.
	toks, err := wf.CreateTokens("waiting", []workflow.TokenData{{"n": 1}, {"n": 2}})
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 2 {
		t.Fatalf("CreateTokens returned %d tokens, want 2", len(toks))
	}
}

func TestListenerErrorsPropagate(t *testing.T) {
	ctx := context.Background()
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("listen", def, "a")
	if err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("boom")

	// A blocking guard-event listener that errors aborts the check.
	h := wf.AddGuardEventListener(func(*workflow.GuardEvent) error { return sentinel })
	if err := wf.ApplyTransitionWithContext(ctx, "t"); err == nil {
		t.Fatal("guard listener error should block the transition")
	}

	// Removing the handle (and removing it again, and a nil handle) is safe.
	wf.RemoveListener(h)
	wf.RemoveListener(h)
	wf.RemoveListener(nil)

	// Now a before-transition listener that errors also aborts.
	wf.AddEventListener(workflow.EventBeforeTransition, func(workflow.Event) error { return sentinel })
	if err := wf.ApplyTransitionWithContext(ctx, "t"); err == nil {
		t.Fatal("before-transition listener error should abort the apply")
	}
}

func TestTokenUnmarshalMalformed(t *testing.T) {
	var tok workflow.Token
	if err := tok.UnmarshalJSON([]byte("{bad")); err == nil {
		t.Fatal("Token.UnmarshalJSON of malformed input should error")
	}
}
