package workflow_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ehabterra/workflow"
)

// batchDef is a small linear CPN: pending -> processing -> done.
func batchDef(t *testing.T) *workflow.Definition {
	t.Helper()
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "processing", "done"},
		[]workflow.Transition{
			*workflow.MustNewTransition("start", []workflow.Place{"pending"}, []workflow.Place{"processing"}),
			*workflow.MustNewTransition("finish", []workflow.Place{"processing"}, []workflow.Place{"done"}),
		},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}
	return def
}

func hasToken(tokens []workflow.Token, id workflow.TokenID) bool {
	for _, tok := range tokens {
		if tok.ID() == id {
			return true
		}
	}
	return false
}

func TestApply_ColoredTokensFlowThroughTransition(t *testing.T) {
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wf.CreateToken("pending", workflow.TokenData{"order": "A", "amount": 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := wf.CreateToken("pending", workflow.TokenData{"order": "B", "amount": 200}); err != nil {
		t.Fatal(err)
	}

	// Fire the whole-marking transition: every token at pending moves to processing.
	if err := wf.ApplyTransition("start"); err != nil {
		t.Fatalf("ApplyTransition(start): %v", err)
	}

	if got := wf.TokenCount("pending"); got != 0 {
		t.Fatalf("pending count = %d, want 0", got)
	}
	if got := wf.TokenCount("processing"); got != 2 {
		t.Fatalf("processing count = %d, want 2 (colored tokens carried, phantom dropped)", got)
	}

	// Token data must survive the move.
	seen := map[string]any{}
	for _, tok := range wf.GetTokens("processing") {
		order, _ := tok.Get("order")
		amount, _ := tok.Get("amount")
		seen[order.(string)] = amount
	}
	if seen["A"] != 100 || seen["B"] != 200 {
		t.Fatalf("token data lost in transition: %+v", seen)
	}
}

// This is the regression that the token-aware move fixes: firing a transition
// must not disturb tokens sitting in places the transition doesn't touch.
func TestApply_UnrelatedPlaceTokensPreserved(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b", "c"},
		[]workflow.Transition{
			*workflow.MustNewTransition("t1", []workflow.Place{"a"}, []workflow.Place{"b"}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("w", def, "a")
	if err != nil {
		t.Fatal(err)
	}

	// A colored token sits in place c, unrelated to transition t1 (a -> b).
	if _, err := wf.CreateToken("c", workflow.TokenData{"keep": true}); err != nil {
		t.Fatal(err)
	}

	if err := wf.ApplyTransition("t1"); err != nil {
		t.Fatalf("ApplyTransition(t1): %v", err)
	}

	// b is now present, a empty — and c's colored token is untouched.
	if !wf.Marking().HasPlace("b") {
		t.Fatal("expected b to be present after t1")
	}
	if wf.TokenCount("a") != 0 {
		t.Fatalf("a count = %d, want 0", wf.TokenCount("a"))
	}
	cTokens := wf.GetTokens("c")
	if len(cTokens) != 1 {
		t.Fatalf("c count = %d, want 1 (unrelated place must be untouched)", len(cTokens))
	}
	if v, _ := cTokens[0].Get("keep"); v != true {
		t.Fatalf("c token lost its data: %+v", cTokens[0].Data())
	}
}

func TestApplyTransitionForToken_AdvancesSingleToken(t *testing.T) {
	ctx := context.Background()
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	tokA, err := wf.CreateToken("pending", workflow.TokenData{"order": "A"})
	if err != nil {
		t.Fatal(err)
	}
	tokB, err := wf.CreateToken("pending", workflow.TokenData{"order": "B"})
	if err != nil {
		t.Fatal(err)
	}

	// Process only token A out of the batch.
	if err := wf.ApplyTransitionForToken(ctx, "start", tokA.ID()); err != nil {
		t.Fatalf("ApplyTransitionForToken: %v", err)
	}

	if !hasToken(wf.GetTokens("processing"), tokA.ID()) {
		t.Fatal("token A should have advanced to processing")
	}
	if hasToken(wf.GetTokens("processing"), tokB.ID()) {
		t.Fatal("token B should NOT have advanced")
	}
	if !hasToken(wf.GetTokens("pending"), tokB.ID()) {
		t.Fatal("token B should remain at pending")
	}
	if got := wf.TokenCount("processing"); got != 1 {
		t.Fatalf("processing count = %d, want 1", got)
	}
}

func TestApplyTransitionForToken_MissingTokenErrors(t *testing.T) {
	ctx := context.Background()
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransitionForToken(ctx, "start", "does-not-exist"); err == nil {
		t.Fatal("expected error for missing token")
	}
}

// Racing to advance the SAME token must consume it exactly once: one caller wins,
// every other gets an error, and processing ends up with a single token.
func TestApplyTransitionForToken_ConcurrentSameTokenConsumedOnce(t *testing.T) {
	ctx := context.Background()
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending") // drop the initial presence token so only token A remains
	tok, err := wf.CreateToken("pending", workflow.TokenData{"order": "A"})
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	var successes int64
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			if err := wf.ApplyTransitionForToken(ctx, "start", tok.ID()); err == nil {
				atomic.AddInt64(&successes, 1)
			}
		}()
	}
	wg.Wait()

	if successes != 1 {
		t.Fatalf("expected exactly 1 successful fire, got %d", successes)
	}
	if got := wf.TokenCount("processing"); got != 1 {
		t.Fatalf("processing token count = %d, want 1 (no duplication)", got)
	}
	if got := wf.TokenCount("pending"); got != 0 {
		t.Fatalf("pending token count = %d, want 0", got)
	}
}

// Racing to advance DIFFERENT tokens must move all of them, with none lost or
// duplicated.
func TestApplyTransitionForToken_ConcurrentDistinctTokens(t *testing.T) {
	ctx := context.Background()
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending")

	const n = 50
	datas := make([]workflow.TokenData, n)
	for i := range datas {
		datas[i] = workflow.TokenData{"i": i}
	}
	tokens, err := wf.CreateTokens("pending", datas)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for _, tok := range tokens {
		go func(id workflow.TokenID) {
			defer wg.Done()
			if err := wf.ApplyTransitionForToken(ctx, "start", id); err != nil {
				t.Errorf("advance %s: %v", id, err)
			}
		}(tok.ID())
	}
	wg.Wait()

	if got := wf.TokenCount("processing"); got != n {
		t.Fatalf("processing token count = %d, want %d", got, n)
	}
	if got := wf.TokenCount("pending"); got != 0 {
		t.Fatalf("pending token count = %d, want 0", got)
	}
}

func TestSelectTokens_Filter(t *testing.T) {
	wf, err := workflow.NewWorkflow("batch", batchDef(t), "pending")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wf.CreateTokens("pending", []workflow.TokenData{
		{"amount": 50}, {"amount": 150}, {"amount": 250},
	}); err != nil {
		t.Fatal(err)
	}

	high := wf.SelectTokens("pending", func(tok workflow.Token) bool {
		v, ok := tok.Get("amount")
		return ok && v.(int) > 100
	})
	if len(high) != 2 {
		t.Fatalf("high-value tokens = %d, want 2", len(high))
	}

	all := wf.SelectTokens("pending", nil)
	// 3 colored + 1 phantom presence token from the initial place.
	if len(all) != 4 {
		t.Fatalf("all tokens = %d, want 4 (3 colored + 1 phantom)", len(all))
	}
}
