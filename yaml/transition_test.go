package yaml_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

func TestApplyTransitionWithHistory_TemplateResolution(t *testing.T) {
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
			"publish_time": "publish_time TEXT",
			"ip_address":   "ip_address TEXT",
			"reason":       "reason TEXT",
		}),
	)
	if err := historyStore.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize history: %v", err)
	}

	// Create workflow with transition that has template variables
	def, _ := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{
			*workflow.MustNewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"}),
		},
	)
	wf, _ := workflow.NewWorkflow("test", def, "start")

	// Add template variables to transition metadata
	transition := def.Transition("to-end")
	transition.SetMetadata("history_custom_fields", map[string]any{
		"publish_time": "now()",
		"ip_address":   "{{request.ip}}",
		"reason":       "{{reason}}",
	})

	// Create context with values using WithTemplateValue
	// This stores values with string keys for template resolution
	ctx := yaml.WithTemplateValue(
		context.Background(),
		"request",
		map[string]any{
			"ip": "192.168.1.1",
		},
	)
	ctx = yaml.WithTemplateValue(ctx, "reason", "Test rejection")

	// Apply transition with history
	err = yaml.ApplyTransitionWithHistory(wf, []workflow.Place{"end"}, historyStore, ctx, "", "", nil)
	if err != nil {
		t.Fatalf("Failed to apply transition: %v", err)
	}

	// Check history
	records, err := historyStore.ListHistory(context.Background(), "test", history.QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to list history: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("Expected 1 record, got %d", len(records))
	}

	record := records[0]
	if record.CustomFields == nil {
		t.Fatal("Expected custom fields to be set")
	}

	// Check resolved values
	if publishTime, ok := record.CustomFields["publish_time"].(string); !ok {
		t.Error("Expected publish_time to be resolved")
	} else {
		// Should be a valid timestamp
		_, err := time.Parse(time.RFC3339, publishTime)
		if err != nil {
			t.Errorf("Expected valid timestamp, got: %s", publishTime)
		}
	}

	if ip, ok := record.CustomFields["ip_address"].(string); !ok || ip != "192.168.1.1" {
		t.Errorf("Expected ip_address='192.168.1.1', got: %v", ip)
	}

	if reason, ok := record.CustomFields["reason"].(string); !ok || reason != "Test rejection" {
		t.Errorf("Expected reason='Test rejection', got: %v", reason)
	}
}
