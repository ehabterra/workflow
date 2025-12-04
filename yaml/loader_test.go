package yaml_test

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

func TestYAMLLoader(t *testing.T) {
	// Create a test YAML config
	yamlContent := `
workflow:
  name: test_workflow
  initial_place: start
  metadata:
    version: "1.0"
  transitions:
    - name: to_end
      from: [start]
      to: [end]
      guard: "workflow.Context('allowed') == true"
      notes: "Test transition"
      actor: "test-user"
      custom_fields:
        test_field: "test_value"
`

	// Write to temp file
	tmpfile, err := os.CreateTemp("", "test_workflow_*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpfile.Name()); err != nil {
			t.Logf("Failed to remove temp file: %v", err)
		}
	}()

	if _, err := tmpfile.Write([]byte(yamlContent)); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	// Load config
	config, err := yaml.LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Validate
	if config.Workflow.Name != "test_workflow" {
		t.Errorf("Expected workflow name 'test_workflow', got '%s'", config.Workflow.Name)
	}

	if config.Workflow.InitialPlace != "start" {
		t.Errorf("Expected initial_place 'start', got '%s'", config.Workflow.InitialPlace)
	}

	if len(config.Workflow.Transitions) != 1 {
		t.Errorf("Expected 1 transition, got %d", len(config.Workflow.Transitions))
	}

	trans := config.Workflow.Transitions[0]
	if trans.Guard != "workflow.Context('allowed') == true" {
		t.Errorf("Expected guard expression, got '%s'", trans.Guard)
	}

	// Load workflow
	loader := yaml.NewLoader()
	wf, err := loader.LoadWorkflow(config, "test-workflow-1")
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	// Test guard expression
	wf.SetContext("allowed", false)
	err = wf.Apply([]workflow.Place{"end"})
	if err == nil {
		t.Error("Expected transition to be blocked when allowed=false")
	}

	wf.SetContext("allowed", true)
	err = wf.Apply([]workflow.Place{"end"})
	if err != nil {
		t.Errorf("Expected transition to succeed when allowed=true, got: %v", err)
	}
}

func TestYAMLLoaderWithHistory(t *testing.T) {
	// Setup database
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	// Create history store
	historyStore := history.NewSQLiteHistory(db,
		history.WithCustomFields(map[string]string{
			"test_field": "test_field TEXT",
		}),
	)
	if err := historyStore.Initialize(); err != nil {
		t.Fatalf("Failed to initialize history: %v", err)
	}

	// Create test YAML
	yamlContent := `
workflow:
  name: test_workflow
  initial_place: start
  transitions:
    - name: to_end
      from: [start]
      to: [end]
      notes: "Test transition"
      actor: "test-user"
      custom_fields:
        test_field: "test_value"
`

	tmpfile, err := os.CreateTemp("", "test_workflow_*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		if err := os.Remove(tmpfile.Name()); err != nil {
			t.Logf("Failed to remove temp file: %v", err)
		}
	}()

	if _, err := tmpfile.Write([]byte(yamlContent)); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	// Load and apply
	config, err := yaml.LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	loader := yaml.NewLoader()
	wf, err := loader.LoadWorkflow(config, "test-workflow-1")
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	ctx := context.Background()
	err = yaml.ApplyTransitionWithHistory(wf, []workflow.Place{"end"}, historyStore, ctx, "", "", nil)
	if err != nil {
		t.Fatalf("Failed to apply transition with history: %v", err)
	}

	// Check history
	records, err := historyStore.ListHistory("test-workflow-1", history.QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to list history: %v", err)
	}

	if len(records) != 1 {
		t.Errorf("Expected 1 history record, got %d", len(records))
	}

	record := records[0]
	if record.Notes != "Test transition" {
		t.Errorf("Expected notes 'Test transition', got '%s'", record.Notes)
	}

	if record.Actor != "test-user" {
		t.Errorf("Expected actor 'test-user', got '%s'", record.Actor)
	}

	if record.CustomFields == nil {
		t.Error("Expected custom fields to be set")
	} else if record.CustomFields["test_field"] != "test_value" {
		t.Errorf("Expected test_field='test_value', got '%v'", record.CustomFields["test_field"])
	}
}
