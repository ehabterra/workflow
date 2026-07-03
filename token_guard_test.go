package workflow_test

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow"
)

// guardDef builds pending -> approved with a guard expression on the transition.
func guardDef(t *testing.T, guard string) *workflow.Definition {
	t.Helper()
	tr, err := workflow.NewTransition("approve", []workflow.Place{"pending"}, []workflow.Place{"approved"})
	if err != nil {
		t.Fatal(err)
	}
	gc, err := workflow.NewExpressionConstraint(guard)
	if err != nil {
		t.Fatalf("compile guard %q: %v", guard, err)
	}
	tr.AddConstraint(gc)

	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "approved"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// The point of token-aware events: a guard can route on the token's own data.
func TestApplyTransitionForToken_GuardRoutesByTokenData(t *testing.T) {
	ctx := context.Background()
	wf, err := workflow.NewWorkflow("w", guardDef(t, "token.amount <= 1000"), "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending")
	small, err := wf.CreateToken("pending", workflow.TokenData{"amount": 500})
	if err != nil {
		t.Fatal(err)
	}
	big, err := wf.CreateToken("pending", workflow.TokenData{"amount": 5000})
	if err != nil {
		t.Fatal(err)
	}

	// small (<= 1000) satisfies the guard and advances.
	if err := wf.ApplyTransitionForToken(ctx, "approve", small.ID()); err != nil {
		t.Fatalf("small token should pass guard: %v", err)
	}
	if !hasToken(wf.GetTokens("approved"), small.ID()) {
		t.Fatal("small token should have advanced to approved")
	}

	// big (> 1000) is blocked by the guard and stays put.
	if err := wf.ApplyTransitionForToken(ctx, "approve", big.ID()); err == nil {
		t.Fatal("big token should be blocked by the guard")
	}
	if !hasToken(wf.GetTokens("pending"), big.ID()) {
		t.Fatal("big token should remain at pending after being blocked")
	}
}

// An event listener can read the tokens involved in a firing.
func TestEvent_ListenerSeesToken(t *testing.T) {
	ctx := context.Background()
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending")
	tok, err := wf.CreateToken("pending", workflow.TokenData{"order": "A"})
	if err != nil {
		t.Fatal(err)
	}

	var seen []workflow.Token
	wf.AddEventListener(workflow.EventAfterTransition, func(e workflow.Event) error {
		seen = append(seen, e.Tokens()...)
		return nil
	})

	if err := wf.ApplyTransitionForToken(ctx, "start", tok.ID()); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0].ID() != tok.ID() {
		t.Fatalf("listener should see the moved token, got %+v", seen)
	}
	if v, _ := seen[0].Get("order"); v != "A" {
		t.Fatalf("listener token data wrong: %v", v)
	}
}

// Whole-marking firing also exposes the moved colored tokens to listeners.
func TestEvent_WholeMarkingExposesMovedTokens(t *testing.T) {
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending")
	if _, err := wf.CreateTokens("pending", []workflow.TokenData{
		{"order": "A"}, {"order": "B"},
	}); err != nil {
		t.Fatal(err)
	}

	var count int
	wf.AddEventListener(workflow.EventAfterTransition, func(e workflow.Event) error {
		count = len(e.Tokens())
		return nil
	})

	if err := wf.ApplyTransition("start"); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("after-event should carry both moved tokens, got %d", count)
	}
}
