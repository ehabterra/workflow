package workflow_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

func TestSetTimeoutAfterNonPositiveClears(t *testing.T) {
	tr := workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})
	tr.SetTimeoutAfter(5 * time.Minute)
	if d, ok := tr.TimeoutAfter(); !ok || d != 5*time.Minute {
		t.Fatalf("TimeoutAfter after set = (%v,%v), want (5m,true)", d, ok)
	}
	// A non-positive duration clears the timeout.
	tr.SetTimeoutAfter(0)
	if d, ok := tr.TimeoutAfter(); ok || d != 0 {
		t.Fatalf("TimeoutAfter after clear = (%v,%v), want (0,false)", d, ok)
	}
	tr.SetTimeoutAfter(-3 * time.Second)
	if _, ok := tr.TimeoutAfter(); ok {
		t.Fatal("negative duration should leave the timeout cleared")
	}
}

func TestMustNewTransitionPanicsOnInvalid(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustNewTransition with an empty name should panic")
		}
	}()
	_ = workflow.MustNewTransition("", []workflow.Place{"a"}, []workflow.Place{"b"})
}

func TestInitialPlaceEmptyMarking(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflowFromMarking("empty", def, workflow.NewMarking(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := wf.InitialPlace(); got != "" {
		t.Fatalf("InitialPlace of an empty-marking workflow = %q, want \"\"", got)
	}
}

func TestMarkingJSONRoundTrip(t *testing.T) {
	// Colored token forces the object serialization form (isSimple == false).
	m := workflow.NewMarking(nil)
	m.AddToken("p", workflow.NewToken(workflow.TokenData{"amount": 100.0}))
	m.AddPlace("q") // uncolored presence token

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal = %v", err)
	}

	back, err := workflow.UnmarshalMarkingJSON(data)
	if err != nil {
		t.Fatalf("UnmarshalMarkingJSON = %v", err)
	}
	if back.TokenCount("p") != 1 || !back.HasPlace("q") {
		t.Fatalf("round-tripped marking lost data: %s", data)
	}

	// Malformed JSON surfaces an error rather than a panic.
	if _, err := workflow.UnmarshalMarkingJSON([]byte("{not json")); err == nil {
		t.Fatal("UnmarshalMarkingJSON of malformed input should error")
	}
}

func TestMarkingSimpleForm(t *testing.T) {
	// Only single uncolored tokens => compact array form.
	m := workflow.NewMarking([]workflow.Place{"draft", "review"})
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if data[0] != '[' {
		t.Fatalf("simple marking should serialize to an array, got %s", data)
	}
	back, err := workflow.UnmarshalMarkingJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !back.HasPlace("draft") || !back.HasPlace("review") {
		t.Fatalf("array-form round trip lost places: %s", data)
	}
}
