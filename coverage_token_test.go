package workflow_test

import (
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestTokenStringAndWith(t *testing.T) {
	tok := workflow.NewToken(nil) // nil data, exercises With's nil-data branch
	if got := tok.String(); !strings.HasPrefix(got, "Token(") || !strings.HasSuffix(got, ")") {
		t.Fatalf("String() = %q, want Token(<id>)", got)
	}
	if !strings.Contains(tok.String(), string(tok.ID())) {
		t.Fatalf("String() = %q, want it to contain the ID %q", tok.String(), tok.ID())
	}

	// With on a token whose data map is nil must allocate a fresh map.
	got := tok.With("k", "v")
	if v, ok := got.Get("k"); !ok || v != "v" {
		t.Fatalf("With then Get = (%v, %v), want (v, true)", v, ok)
	}
	// Receiver is unchanged.
	if _, ok := tok.Get("k"); ok {
		t.Fatal("With mutated the receiver")
	}
}

func TestAggregateTokensNumericCoercion(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"p"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"p"}, []workflow.Place{"p"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("agg", def, "p")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("p")

	// One token per numeric kind so toFloat's every case is exercised, plus a
	// non-numeric value that toFloat must reject.
	datas := []workflow.TokenData{
		{"n": int(1)},
		{"n": int8(2)},
		{"n": int16(3)},
		{"n": int32(4)},
		{"n": int64(5)},
		{"n": uint(6)},
		{"n": uint8(7)},
		{"n": uint16(8)},
		{"n": uint32(9)},
		{"n": uint64(10)},
		{"n": float32(11)},
		{"n": float64(12)},
		{"n": "not-a-number"},
		{"other": 99}, // lacks the field entirely
	}
	for _, d := range datas {
		if _, err := wf.CreateToken("p", d); err != nil {
			t.Fatal(err)
		}
	}

	agg := wf.AggregateTokens(nil, "n")
	if agg.Count != 12 {
		t.Fatalf("Count = %d, want 12 (non-numeric and missing-field ignored)", agg.Count)
	}
	if agg.Sum != 78 { // 1+2+...+12
		t.Fatalf("Sum = %v, want 78", agg.Sum)
	}
	if agg.Min != 1 || agg.Max != 12 {
		t.Fatalf("Min/Max = %v/%v, want 1/12", agg.Min, agg.Max)
	}
	if agg.Avg != 6.5 {
		t.Fatalf("Avg = %v, want 6.5", agg.Avg)
	}
}

func TestWorkflowTokenAccessors(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"p"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"p"}, []workflow.Place{"p"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("acc", def, "p")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("p")
	tok, err := wf.CreateToken("p", workflow.TokenData{"x": 1})
	if err != nil {
		t.Fatal(err)
	}

	all := wf.AllTokens()
	if len(all["p"]) != 1 {
		t.Fatalf("AllTokens[p] = %v, want one token", all["p"])
	}

	if err := wf.RemoveToken("p", tok.ID()); err != nil {
		t.Fatalf("RemoveToken = %v, want nil", err)
	}
	if wf.TokenCount("p") != 0 {
		t.Fatalf("TokenCount after remove = %d, want 0", wf.TokenCount("p"))
	}

	// ListenerCount reflects instance-level listeners only.
	if n := wf.ListenerCount(workflow.EventAfterTransition); n != 0 {
		t.Fatalf("ListenerCount before adding = %d, want 0", n)
	}
	wf.AddEventListener(workflow.EventAfterTransition, func(workflow.Event) error { return nil })
	if n := wf.ListenerCount(workflow.EventAfterTransition); n != 1 {
		t.Fatalf("ListenerCount after adding = %d, want 1", n)
	}
}
