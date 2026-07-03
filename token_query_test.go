package workflow_test

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow"
)

func queryWF(t *testing.T) *workflow.Workflow {
	t.Helper()
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "processing", "done"},
		[]workflow.Transition{
			*workflow.MustNewTransition("start", []workflow.Place{"pending"}, []workflow.Place{"processing"}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("orders", def, "pending")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pending")
	if _, err := wf.CreateTokens("pending", []workflow.TokenData{
		{"order": "A", "amount": 100.0},
		{"order": "B", "amount": 250.0},
		{"order": "C", "amount": 50.0},
	}); err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestCountTokens(t *testing.T) {
	wf := queryWF(t)

	if got := wf.CountTokens(nil); got != 3 {
		t.Fatalf("CountTokens(nil) = %d, want 3", got)
	}
	high := wf.CountTokens(func(tok workflow.Token) bool {
		v, _ := tok.Get("amount")
		return v.(float64) >= 100
	})
	if high != 2 {
		t.Fatalf("high-value count = %d, want 2", high)
	}
}

func TestFindTokens_GroupedByPlace(t *testing.T) {
	wf := queryWF(t)
	if err := wf.ApplyTransitionForToken(context.Background(), "start", wf.GetTokens("pending")[0].ID()); err != nil {
		t.Fatalf("advance one: %v", err)
	}

	found := wf.FindTokens(nil)
	if len(found["pending"]) != 2 || len(found["processing"]) != 1 {
		t.Fatalf("FindTokens grouping wrong: pending=%d processing=%d",
			len(found["pending"]), len(found["processing"]))
	}
}

func TestAggregateTokens(t *testing.T) {
	wf := queryWF(t)

	agg := wf.AggregateTokens(nil, "amount")
	if agg.Count != 3 {
		t.Fatalf("Count = %d, want 3", agg.Count)
	}
	if agg.Sum != 400 {
		t.Fatalf("Sum = %v, want 400", agg.Sum)
	}
	if agg.Min != 50 || agg.Max != 250 {
		t.Fatalf("Min/Max = %v/%v, want 50/250", agg.Min, agg.Max)
	}
	if agg.Avg != 400.0/3.0 {
		t.Fatalf("Avg = %v, want %v", agg.Avg, 400.0/3.0)
	}
}

func TestAggregateTokens_IgnoresNonNumericAndMissing(t *testing.T) {
	def, _ := workflow.NewDefinition([]workflow.Place{"p"}, []workflow.Transition{
		*workflow.MustNewTransition("t", []workflow.Place{"p"}, []workflow.Place{"p"}),
	})
	wf, _ := workflow.NewWorkflow("w", def, "p")
	wf.ClearPlace("p")
	_, _ = wf.CreateTokens("p", []workflow.TokenData{
		{"amount": 10.0},
		{"amount": "not-a-number"},
		{"other": 5.0}, // missing amount
	})

	agg := wf.AggregateTokens(nil, "amount")
	if agg.Count != 1 || agg.Sum != 10 {
		t.Fatalf("aggregate should ignore non-numeric/missing: %+v", agg)
	}
}

func TestTransformTokens(t *testing.T) {
	wf := queryWF(t)

	// Apply a 10% surcharge to high-value orders, keeping identity.
	ids := map[workflow.TokenID]bool{}
	for _, tok := range wf.GetTokens("pending") {
		ids[tok.ID()] = true
	}

	n := wf.TransformTokens("pending",
		func(tok workflow.Token) bool {
			v, _ := tok.Get("amount")
			return v.(float64) >= 100
		},
		func(tok workflow.Token) workflow.TokenData {
			d := tok.Data()
			d["amount"] = d["amount"].(float64) * 1.1
			return d
		},
	)
	if n != 2 {
		t.Fatalf("transformed = %d, want 2", n)
	}

	// Identities preserved, count unchanged, values updated.
	if wf.TokenCount("pending") != 3 {
		t.Fatalf("token count changed after transform: %d", wf.TokenCount("pending"))
	}
	agg := wf.AggregateTokens(nil, "amount")
	// 100*1.1 + 250*1.1 + 50 = 110 + 275 + 50 = 435
	if agg.Sum != 435 {
		t.Fatalf("Sum after transform = %v, want 435", agg.Sum)
	}
	for _, tok := range wf.GetTokens("pending") {
		if !ids[tok.ID()] {
			t.Fatalf("transform changed a token identity: %s", tok.ID())
		}
	}
}
