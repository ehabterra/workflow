package main

import (
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/workflowtest"
	"github.com/ehabterra/workflow/yaml"
)

// TestWorkflowYAML pins the modernized net: the strict loader accepts it,
// the single OR-input rejection replaces the seven per-stage twins, its
// reset arcs clear every sibling review token, and the diagram renders the
// team lanes and gateway notation.
func TestWorkflowYAML(t *testing.T) {
	cfg, err := yaml.LoadConfig("workflow.yaml")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	def, err := yaml.NewLoader().LoadDefinition(cfg)
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	// ONE rejection, OR-input, resetting all review places.
	reject := def.Transition("reject_project")
	if reject == nil {
		t.Fatal("reject_project missing")
	}
	if !reject.FromAny() {
		t.Fatal("reject_project must be an OR-input (from_any) transition")
	}
	if len(reject.Resets()) != 7 {
		t.Fatalf("reject_project resets %v, want all 7 review places", reject.Resets())
	}
	for _, gone := range []string{"reject_qa_failures", "reject_after_legal_complete", "reject_design_issues"} {
		if def.Transition(gone) != nil {
			t.Fatalf("collapsed twin %s still present", gone)
		}
	}

	// Rejecting from one branch cancels the parallel siblings — no stranded
	// tokens (this is what the reset arcs buy over the old twins).
	m := workflow.NewMarking([]workflow.Place{"qa_testing", "security_review", "legal_review"})
	wf, err := workflow.NewWorkflowFromMarking("wf", def, m)
	if err != nil {
		t.Fatal(err)
	}
	wf.SetContext("roles", []string{"admin"})
	workflowtest.Apply(t, wf, "reject_project")
	workflowtest.AssertMarking(t, wf, "rejected")

	// The diagram carries the new visual language: team lanes, the OR-input
	// exclusive gateway, and reset "cancels" edges.
	diagram := def.Diagram()
	for _, want := range []string{
		`subgraph grp_QA ["QA"]`,
		`subgraph grp_Finance ["Finance"]`,
		`j_reject_project{"×"}`, // ◇× exclusive gateway
		"cancels",               // reset arcs
	} {
		if !strings.Contains(diagram, want) {
			t.Fatalf("diagram missing %q:\n%s", want, diagram)
		}
	}
	if !strings.HasPrefix(def.Diagram(workflow.DiagramDirectionLeftRight), "flowchart LR\n") {
		t.Fatal("direction override must render flowchart LR")
	}
}
