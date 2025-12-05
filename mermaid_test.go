package workflow_test

import (
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestDiagramWithGuards(t *testing.T) {
	// Create a workflow with guards
	t1, _ := workflow.NewTransition("submit", []workflow.Place{"start"}, []workflow.Place{"review"})
	t1.SetMetadata("guard", "hasRole('manager')")

	t2, _ := workflow.NewTransition("approve", []workflow.Place{"review"}, []workflow.Place{"approved"})
	t2.SetMetadata("guard", "hasRole('admin') or hasRole('manager')")

	t3, _ := workflow.NewTransition("reject", []workflow.Place{"review"}, []workflow.Place{"rejected"})
	t3.SetMetadata("guard", "hasRole('admin') and workflow.Context('can_reject') == true")

	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "review", "approved", "rejected"},
		[]workflow.Transition{*t1, *t2, *t3},
	)
	if err != nil {
		t.Fatalf("failed to create definition: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	diagram := wf.Diagram()

	// Check that guards are included in the diagram
	// Look for common guard indicators: role, permission, in place, or context conditions
	hasGuardInfo := strings.Contains(diagram, "role:") ||
		strings.Contains(diagram, "roles:") ||
		strings.Contains(diagram, "permission:") ||
		strings.Contains(diagram, "in place:") ||
		strings.Contains(diagram, "==") ||
		strings.Contains(diagram, "!=")

	if !hasGuardInfo {
		t.Logf("Diagram output:\n%s", diagram)
		t.Error("Diagram should contain guard information")
	}

	// Check that transition names are present
	if !strings.Contains(diagram, "submit") {
		t.Error("Diagram should contain submit transition")
	}
	if !strings.Contains(diagram, "approve") {
		t.Error("Diagram should contain approve transition")
	}
	if !strings.Contains(diagram, "reject") {
		t.Error("Diagram should contain reject transition")
	}
}
