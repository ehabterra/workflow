// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"strings"
	"testing"

	wfyaml "github.com/ehabterra/workflow/yaml"
)

const effectsYAML = `
workflow:
  name: approval
  initial_marking: open
  places:
    - name: open
    - name: done
  transitions:
    - name: approve
      from: [open]
      to: [done]
      effects:
        - name: audit
          params: {action: "approve"}
        - name: outbox
          params: {event: "approved"}
      after_commit:
        - name: email
          params: {template: "approved"}
`

func TestLoadDeclaredEffects(t *testing.T) {
	cfg, err := wfyaml.LoadConfigFromBytes([]byte(effectsYAML))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	def, err := wfyaml.NewLoader().LoadDefinition(cfg)
	if err != nil {
		t.Fatalf("load definition: %v", err)
	}
	tr := def.Transition("approve")
	if tr == nil {
		t.Fatal("transition not found")
	}

	effects := tr.Effects()
	if len(effects) != 2 {
		t.Fatalf("effects = %d, want 2", len(effects))
	}
	// Declared order is execution order and must survive the load unsorted.
	if effects[0].Name != "audit" || effects[1].Name != "outbox" {
		t.Errorf("effect order not preserved: %v, %v", effects[0].Name, effects[1].Name)
	}
	if effects[0].Params["action"] != "approve" {
		t.Errorf("params[action] = %v", effects[0].Params["action"])
	}

	after := tr.AfterCommit()
	if len(after) != 1 || after[0].Name != "email" {
		t.Fatalf("after_commit = %v, want [email]", after)
	}
	if after[0].Params["template"] != "approved" {
		t.Errorf("params[template] = %v", after[0].Params["template"])
	}
}

// TestEffectsAreIsolatedFromCallerMutation: Effects() returns a copy, so a
// caller cannot rewrite a loaded definition's declared effects in place.
func TestEffectsAreIsolatedFromCallerMutation(t *testing.T) {
	cfg, err := wfyaml.LoadConfigFromBytes([]byte(effectsYAML))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	def, err := wfyaml.NewLoader().LoadDefinition(cfg)
	if err != nil {
		t.Fatalf("load definition: %v", err)
	}
	tr := def.Transition("approve")
	got := tr.Effects()
	got[0].Name = "tampered"
	got[0].Params["action"] = "tampered"

	fresh := tr.Effects()
	if fresh[0].Name != "audit" || fresh[0].Params["action"] != "approve" {
		t.Errorf("declared effects were mutated through the returned slice: %+v", fresh[0])
	}
}

// TestEmptyEffectNameRejected: an unnamed effect can never resolve, so it must
// fail at load rather than the first time that transition fires.
func TestEmptyEffectNameRejected(t *testing.T) {
	bad := strings.Replace(effectsYAML, `        - name: audit`, `        - name: ""`, 1)
	cfg, err := wfyaml.LoadConfigFromBytes([]byte(bad))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if _, err := wfyaml.NewLoader().LoadDefinition(cfg); err == nil {
		t.Fatal("expected an empty effect name to be rejected at load")
	}
}
