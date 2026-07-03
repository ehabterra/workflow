package yaml_test

import (
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

var yamlTimerEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// TestYAML_AfterIsAuthoritativeOverMetadata proves the timeout has a single
// source of truth: with both `after: 72h` and a conflicting metadata {after: 1h},
// the engine's deadline is 72h, TimeoutAfter reports 72h, and the diagram (which
// derives its label from TimeoutAfter, not metadata) shows 72h.
func TestYAML_AfterIsAuthoritativeOverMetadata(t *testing.T) {
	cfg := &yaml.Config{
		Workflow: yaml.WorkflowConfig{
			Name:           "wf",
			InitialMarking: yaml.InitialMarkingConfig{Places: map[string][]map[string]any{"submitted": nil}},
			Transitions: []yaml.TransitionConfig{
				{Name: "approve", From: []string{"submitted"}, To: []string{"approved"}},
				{
					Name:     "escalate",
					From:     []string{"submitted"},
					To:       []string{"escalated"},
					After:    "72h",
					Metadata: map[string]any{"after": "1h"}, // stray, must be ignored by the engine
				},
			},
		},
	}

	loader := yaml.NewLoader()
	def, err := loader.LoadDefinition(cfg)
	if err != nil {
		t.Fatalf("LoadDefinition: %v", err)
	}

	// TimeoutAfter is authoritative: 72h, not the metadata's 1h.
	d, ok := def.Transition("escalate").TimeoutAfter()
	if !ok || d != 72*time.Hour {
		t.Fatalf("TimeoutAfter = %v,%v, want 72h,true (after: is authoritative)", d, ok)
	}

	wf, err := workflow.NewWorkflow("wf", def, "submitted", workflow.WithClock(func() time.Time { return yamlTimerEpoch }))
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	// Not due at 1h (the stray metadata value); due at 72h.
	if due := wf.Due(yamlTimerEpoch.Add(time.Hour)); len(due) != 0 {
		t.Fatalf("Due(+1h) = %d, want none (metadata 1h must not drive the engine)", len(due))
	}
	due := wf.Due(yamlTimerEpoch.Add(72 * time.Hour))
	if len(due) != 1 || due[0].Name() != "escalate" {
		t.Fatalf("Due(+72h) = %v, want [escalate]", due)
	}

	// The diagram surfaces the real timeout (72h), derived from TimeoutAfter.
	diagram := wf.Diagram()
	if !strings.Contains(diagram, "72h") {
		t.Fatalf("Diagram does not show the 72h timer:\n%s", diagram)
	}
}

func TestYAML_AfterErrorPaths(t *testing.T) {
	mkCfg := func(after string) *yaml.Config {
		return &yaml.Config{
			Workflow: yaml.WorkflowConfig{
				Name:           "wf",
				InitialMarking: yaml.InitialMarkingConfig{Places: map[string][]map[string]any{"a": nil}},
				Transitions: []yaml.TransitionConfig{
					{Name: "t", From: []string{"a"}, To: []string{"b"}, After: after},
				},
			},
		}
	}
	loader := yaml.NewLoader()

	// Unparseable duration is rejected.
	if _, err := loader.LoadDefinition(mkCfg("notaduration")); err == nil {
		t.Fatal("LoadDefinition with invalid after: want error, got nil")
	}
	// A non-positive duration is rejected (a timer must be positive).
	if _, err := loader.LoadDefinition(mkCfg("0s")); err == nil {
		t.Fatal("LoadDefinition with zero after: want error, got nil")
	}
	if _, err := loader.LoadDefinition(mkCfg("-5m")); err == nil {
		t.Fatal("LoadDefinition with negative after: want error, got nil")
	}
	// A valid positive duration is accepted and authoritative.
	def, err := loader.LoadDefinition(mkCfg("30m"))
	if err != nil {
		t.Fatalf("LoadDefinition(30m): %v", err)
	}
	if d, ok := def.Transition("t").TimeoutAfter(); !ok || d != 30*time.Minute {
		t.Fatalf("TimeoutAfter = %v,%v, want 30m,true", d, ok)
	}
}
