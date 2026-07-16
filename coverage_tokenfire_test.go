package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestApplyTransitionForTokenErrorPaths(t *testing.T) {
	ctx := context.Background()

	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "approved"},
		[]workflow.Transition{*workflow.MustNewTransition("approve", []workflow.Place{"pending"}, []workflow.Place{"approved"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("w", def, "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending")
	tok, err := wf.CreateToken("pending", workflow.TokenData{"x": 1})
	if err != nil {
		t.Fatal(err)
	}

	// Unknown transition => ErrTransitionNotFound.
	if err := wf.ApplyTransitionForToken(ctx, "nope", tok.ID()); !errors.Is(err, workflow.ErrTransitionNotFound) {
		t.Fatalf("unknown transition = %v, want ErrTransitionNotFound", err)
	}

	// Unknown token ID => ErrTokenNotFound.
	if err := wf.ApplyTransitionForToken(ctx, "approve", "no-such-token"); !errors.Is(err, workflow.ErrTokenNotFound) {
		t.Fatalf("unknown token = %v, want ErrTokenNotFound", err)
	}

	// Happy path advances the token.
	if err := wf.ApplyTransitionForToken(ctx, "approve", tok.ID()); err != nil {
		t.Fatalf("valid per-token fire = %v", err)
	}
	if wf.TokenCount("approved") != 1 {
		t.Fatalf("approved count = %d, want 1", wf.TokenCount("approved"))
	}
}

func TestApplyTransitionForTokenMultiInputRejected(t *testing.T) {
	ctx := context.Background()
	// A non-OR transition with two input places cannot be fired per-token.
	tr := workflow.MustNewTransition("join", []workflow.Place{"a", "b"}, []workflow.Place{"c"})
	def, err := workflow.NewDefinition([]workflow.Place{"a", "b", "c"}, []workflow.Transition{*tr})
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("multi", def, "a")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("a")
	tok, err := wf.CreateToken("a", workflow.TokenData{"x": 1})
	if err != nil {
		t.Fatal(err)
	}

	err = wf.ApplyTransitionForToken(ctx, "join", tok.ID())
	if !errors.Is(err, workflow.ErrInvalidTransition) {
		t.Fatalf("multi-input per-token fire = %v, want ErrInvalidTransition", err)
	}
}
