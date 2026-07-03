package yaml_test

import (
	"testing"

	"github.com/ehabterra/workflow/yaml"
)

func TestLoadConfigFromBytes(t *testing.T) {
	// Test with valid YAML
	validYAML := `
workflow:
  name: test_workflow
  initial_marking: start
  transitions:
    - name: to_end
      from: [start]
      to: [end]
`

	config, err := yaml.LoadConfigFromBytes([]byte(validYAML))
	if err != nil {
		t.Fatalf("LoadConfigFromBytes() with valid YAML should not error, got %v", err)
	}
	if config == nil {
		t.Fatal("LoadConfigFromBytes() should not return nil")
	}
	if config.Workflow.Name != "test_workflow" {
		t.Errorf("LoadConfigFromBytes() workflow name = %s, want 'test_workflow'", config.Workflow.Name)
	}

	// Test with invalid YAML
	invalidYAML := `invalid: yaml: content: [`
	_, err = yaml.LoadConfigFromBytes([]byte(invalidYAML))
	if err == nil {
		t.Error("LoadConfigFromBytes() with invalid YAML should error")
	}

	// Test with missing workflow name (validation error)
	missingNameYAML := `
workflow:
  initial_marking: start
  transitions:
    - name: to_end
      from: [start]
      to: [end]
`
	_, err = yaml.LoadConfigFromBytes([]byte(missingNameYAML))
	if err == nil {
		t.Error("LoadConfigFromBytes() with missing workflow name should error")
	}
}
