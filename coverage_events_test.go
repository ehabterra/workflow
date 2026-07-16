package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestNewWorkflowFromMarkingValidation(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := workflow.NewWorkflowFromMarking("", def, workflow.NewMarking(nil)); !errors.Is(err, workflow.ErrInvalidWorkflow) {
		t.Fatalf("empty name = %v, want ErrInvalidWorkflow", err)
	}
	if _, err := workflow.NewWorkflowFromMarking("w", nil, workflow.NewMarking(nil)); !errors.Is(err, workflow.ErrInvalidDefinition) {
		t.Fatalf("nil definition = %v, want ErrInvalidDefinition", err)
	}
	if _, err := workflow.NewWorkflowFromMarking("w", def, nil); !errors.Is(err, workflow.ErrInvalidMarking) {
		t.Fatalf("nil marking = %v, want ErrInvalidMarking", err)
	}
	// A marking referencing an undefined place is rejected.
	bad := workflow.NewMarking([]workflow.Place{"ghost"})
	if _, err := workflow.NewWorkflowFromMarking("w", def, bad); !errors.Is(err, workflow.ErrInvalidPlace) {
		t.Fatalf("undefined place = %v, want ErrInvalidPlace", err)
	}
}

func TestDefinitionAndManagerListenerErrorsAbortApply(t *testing.T) {
	ctx := context.Background()
	sentinel := errors.New("nope")

	// Definition-level listener error aborts the apply (fireEvent def branch).
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	def.AddEventListener(workflow.EventBeforeTransition, func(workflow.Event) error { return sentinel })
	wf, err := workflow.NewWorkflow("def-listen", def, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransitionWithContext(ctx, "t"); err == nil {
		t.Fatal("definition-level listener error should abort the apply")
	}

	// Manager-level listener error aborts too (fireEvent manager branch). A
	// managed workflow routes events through the manager's listeners.
	def2, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	mgr := workflow.NewManager(workflow.NewRegistry(), NewMockStorage())
	mgr.AddEventListener(workflow.EventBeforeTransition, func(workflow.Event) error { return sentinel })
	wf2, err := workflow.NewWorkflow("mgr-listen", def2, "a")
	if err != nil {
		t.Fatal(err)
	}
	wf2.SetManager(mgr)
	if err := wf2.ApplyTransitionWithContext(ctx, "t"); err == nil {
		t.Fatal("manager-level listener error should abort the apply")
	}
}
