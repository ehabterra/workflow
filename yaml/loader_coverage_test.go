package yaml_test

import (
	"testing"

	"github.com/ehabterra/workflow/yaml"
)

func mustConfig(t *testing.T, src string) *yaml.Config {
	t.Helper()
	cfg, err := yaml.LoadConfigFromBytes([]byte(src))
	if err != nil {
		t.Fatalf("config parse (should be valid pre-load): %v", err)
	}
	return cfg
}

func TestLoadDefinitionInvalidGuard(t *testing.T) {
	// Valid schema, but the guard expression does not compile.
	cfg := mustConfig(t, "workflow:\n  name: w\n  initial_marking: a\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n      guard: \"1 +\"\n")
	if _, err := yaml.NewLoader().LoadDefinition(cfg); err == nil {
		t.Fatal("an uncompilable guard should fail LoadDefinition")
	}
	// The error also surfaces through LoadWorkflow.
	if _, err := yaml.NewLoader().LoadWorkflow(cfg, "id"); err == nil {
		t.Fatal("LoadWorkflow should propagate the guard-compile error")
	}
}

func TestLoadDefinitionInvalidTransition(t *testing.T) {
	// Duplicate 'from' place passes config validation but fails NewTransition.
	cfg := mustConfig(t, "workflow:\n  name: w\n  initial_marking: a\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a, a]\n      to: [b]\n")
	if _, err := yaml.NewLoader().LoadDefinition(cfg); err == nil {
		t.Fatal("a duplicate 'from' place should fail LoadDefinition")
	}
}
