package yaml_test

import (
	"strings"
	"testing"

	"github.com/ehabterra/workflow/yaml"
)

// TestResetsFromYAML: the `resets:` key wires reset arcs (cancellation
// regions) through the strict loader.
func TestResetsFromYAML(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(`
workflow:
  name: cancel_demo
  initial_marking: start
  transitions:
    - name: split
      from: [start]
      to: [a, b]
    - name: reject_a
      from: [a]
      to: [rejected]
      resets: [b]
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "wf")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if got := wf.Definition().Transition("reject_a").Resets(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("resets not wired, got %v", got)
	}

	// End to end: firing the reject clears the sibling branch.
	if err := wf.ApplyTransition("split"); err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("reject_a"); err != nil {
		t.Fatal(err)
	}
	places := wf.Marking().Places()
	if len(places) != 1 || places[0] != "rejected" {
		t.Fatalf("want only rejected after the reset, got %v", places)
	}
}

// TestResetsValidation: a reset naming an undefined place is rejected when
// places are declared explicitly.
func TestResetsValidation(t *testing.T) {
	_, err := yaml.LoadConfigFromBytes([]byte(`
workflow:
  name: bad
  initial_marking: a
  places:
    - name: a
    - name: b
  transitions:
    - name: t
      from: [a]
      to: [b]
      resets: [ghost]
`))
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("want validation error naming ghost, got %v", err)
	}
}

// TestFromAnyFromYAML: the `from_any:` key wires OR-input semantics.
func TestFromAnyFromYAML(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(`
workflow:
  name: or_demo
  initial_marking: pending
  transitions:
    - name: escalate
      from: [pending]
      to: [escalated]
    - name: approve
      from: [pending, escalated]
      to: [done]
      from_any: true
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "wf")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !wf.Definition().Transition("approve").FromAny() {
		t.Fatal("from_any not wired")
	}
	if err := wf.ApplyTransition("escalate"); err != nil {
		t.Fatal(err)
	}
	if err := wf.ApplyTransition("approve"); err != nil {
		t.Fatalf("approve from escalated stage: %v", err)
	}
	if places := wf.Marking().Places(); len(places) != 1 || places[0] != "done" {
		t.Fatalf("want [done], got %v", places)
	}
}
