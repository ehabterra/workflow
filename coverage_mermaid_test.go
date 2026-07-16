// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
)

// TestDiagramRichFeatures renders a definition exercising the timer, reset-arc,
// diagram_class, and region-grouping branches of the Mermaid renderer.
func TestDiagramRichFeatures(t *testing.T) {
	// Timed transition with a diagram_class, plus a reset arc that cancels a place.
	escalate := workflow.MustNewTransition("escalate", []workflow.Place{"waiting"}, []workflow.Place{"escalated"})
	escalate.SetTimeoutAfter(30 * time.Minute)
	escalate.SetMetadata("diagram_class", "auto")

	cancel := workflow.MustNewTransition("cancel", []workflow.Place{"waiting"}, []workflow.Place{"done"})
	cancel.SetResets("escalated") // dotted "cancels" reset arc
	cancel.SetMetadata("diagram_class", "person")

	def, err := workflow.NewDefinition(
		[]workflow.Place{"waiting", "escalated", "done"},
		[]workflow.Transition{*escalate, *cancel},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Group two places into a region so the subgraph branch runs.
	def.SetPlaceMetadata("waiting", map[string]any{"diagram_group": "Queue"})
	def.SetPlaceMetadata("escalated", map[string]any{"diagram_group": "Queue"})

	diagram := def.Diagram()

	for _, want := range []string{
		"flowchart",             // renderer header
		"⏱",                     // timer label prefix
		"class t_escalate auto", // diagram_class metadata
		"class t_cancel person",
		"cancels",   // reset arc
		"linkStyle", // timer/reset edge styling
		"subgraph",  // region grouping
	} {
		if !strings.Contains(diagram, want) {
			t.Errorf("diagram missing %q\n---\n%s", want, diagram)
		}
	}
}
