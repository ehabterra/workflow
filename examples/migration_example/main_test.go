package main_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	main "github.com/ehabterra/workflow/examples/migration_example"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

// TestMigrationWorkflow tests that workflows work correctly with database migrations
func TestMigrationWorkflow(t *testing.T) {
	// Use a test database
	dbPath := "./test_migration.db"
	os.Remove(dbPath) // Clean up
	defer os.Remove(dbPath)

	// Open database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := main.RunMigrations(db); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Configure storage with all migrated fields
	sqlStore, err := storage.NewSQLiteStorage(db,
		storage.WithTable("workflow_states"),
		storage.WithCustomFields(map[string]string{
			"title":      "title TEXT",
			"content":    "content TEXT",
			"created_at": "created_at DATETIME",
			"updated_at": "updated_at DATETIME",
			"priority":   "priority INTEGER",
			"tags":       "tags TEXT",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create workflow definition
	workflowDef, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{
			*workflow.MustNewTransition("complete", []workflow.Place{"start"}, []workflow.Place{"end"}),
		},
	)
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Create manager
	registry := workflow.NewRegistry()
	workflowMgr := workflow.NewManager(registry, sqlStore)

	// Test: Create workflow with all fields
	wf, err := workflowMgr.CreateWorkflow(context.Background(), "test-1", workflowDef, "start")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Set all context fields (including migrated ones)
	wf.SetContext("title", "Test Document")
	wf.SetContext("content", "Test Content")
	wf.SetContext("priority", 10)
	wf.SetContext("tags", "test,important")
	now := time.Now().Format(time.RFC3339)
	wf.SetContext("created_at", now)
	wf.SetContext("updated_at", now)

	// Save workflow
	if err := workflowMgr.SaveWorkflow(context.Background(), "test-1", wf); err != nil {
		t.Fatalf("Failed to save workflow: %v", err)
	}

	// Test: Load workflow and verify all fields are preserved
	loadedPlaces, loadedCtx, err := sqlStore.LoadState(context.Background(), "test-1")
	if err != nil {
		t.Fatalf("Failed to load state: %v", err)
	}

	if p := loadedPlaces.Places(); len(p) != 1 || p[0] != "start" {
		t.Errorf("Expected places [start], got %v", loadedPlaces)
	}

	// Verify all context fields
	if loadedCtx["title"] != "Test Document" {
		t.Errorf("Expected title 'Test Document', got %v", loadedCtx["title"])
	}
	if loadedCtx["content"] != "Test Content" {
		t.Errorf("Expected content 'Test Content', got %v", loadedCtx["content"])
	}
	if loadedCtx["priority"] != int64(10) { // SQLite returns int64 for INTEGER
		t.Errorf("Expected priority 10, got %v", loadedCtx["priority"])
	}
	if loadedCtx["tags"] != "test,important" {
		t.Errorf("Expected tags 'test,important', got %v", loadedCtx["tags"])
	}

	// Test: Apply transition and update timestamp
	wf, err = workflowMgr.GetWorkflow(context.Background(), "test-1", workflowDef)
	if err != nil {
		t.Fatalf("Failed to get workflow: %v", err)
	}

	if err := wf.Apply([]workflow.Place{"end"}); err != nil {
		t.Fatalf("Failed to apply transition: %v", err)
	}

	wf.SetContext("updated_at", time.Now().Format(time.RFC3339))
	if err := workflowMgr.SaveWorkflow(context.Background(), "test-1", wf); err != nil {
		t.Fatalf("Failed to save workflow after transition: %v", err)
	}

	// Verify state changed
	loadedPlaces, _, err = sqlStore.LoadState(context.Background(), "test-1")
	if err != nil {
		t.Fatalf("Failed to load state after transition: %v", err)
	}
	if p := loadedPlaces.Places(); len(p) != 1 || p[0] != "end" {
		t.Errorf("Expected places [end] after transition, got %v", loadedPlaces)
	}
}

// TestMigrationHistory tests that history works with migrated schema
func TestMigrationHistory(t *testing.T) {
	dbPath := "./test_history_migration.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Run migrations
	if err := main.RunMigrations(db); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Configure history with migrated fields
	historyStore := history.NewSQLiteHistory(db,
		history.WithTable("transition_history"),
		history.WithCustomFields(map[string]string{
			"duration_ms": "duration_ms INTEGER",
			"metadata":    "metadata TEXT",
		}),
	)

	// Save transition with custom fields
	record := &history.TransitionRecord{
		WorkflowID: "test-wf",
		FromState:  "start",
		ToState:    "end",
		Transition: "complete",
		Notes:      "Test transition",
		Actor:      "test-user",
		CreatedAt:  time.Now(),
		CustomFields: map[string]any{
			"duration_ms": 150,
			"metadata":    `{"key":"value"}`,
		},
	}

	if err := historyStore.SaveTransition(context.Background(), record); err != nil {
		t.Fatalf("Failed to save transition: %v", err)
	}

	// Query history
	historyRecords, err := historyStore.ListHistory(context.Background(), "test-wf", history.QueryOptions{})
	if err != nil {
		t.Fatalf("Failed to list history: %v", err)
	}

	if len(historyRecords) != 1 {
		t.Fatalf("Expected 1 history record, got %d", len(historyRecords))
	}

	rec := historyRecords[0]
	if rec.WorkflowID != "test-wf" {
		t.Errorf("Expected workflow_id 'test-wf', got %s", rec.WorkflowID)
	}
	if rec.CustomFields == nil {
		t.Fatal("Expected custom fields to be present")
	}
	if rec.CustomFields["duration_ms"] != int64(150) {
		t.Errorf("Expected duration_ms 150, got %v", rec.CustomFields["duration_ms"])
	}
	if rec.CustomFields["metadata"] != `{"key":"value"}` {
		t.Errorf("Expected metadata '{\"key\":\"value\"}', got %v", rec.CustomFields["metadata"])
	}
}

// TestMigrationBackwardCompatibility tests that workflows created before migrations
// continue to work after migrations are applied
func TestMigrationBackwardCompatibility(t *testing.T) {
	dbPath := "./test_backward_compat.db"
	os.Remove(dbPath)
	defer os.Remove(dbPath)

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Step 1: Create workflow with initial schema (simulating old version)
	// We'll manually create the initial table structure
	_, err = db.Exec(`
		CREATE TABLE workflow_states (
			id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			version INTEGER NOT NULL DEFAULT 0,
			context TEXT NOT NULL DEFAULT '{}',
			title TEXT,
			content TEXT
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create initial schema: %v", err)
	}

	// Create storage with initial fields only
	sqlStore, err := storage.NewSQLiteStorage(db,
		storage.WithTable("workflow_states"),
		storage.WithCustomFields(map[string]string{
			"title":   "title TEXT",
			"content": "content TEXT",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create storage: %v", err)
	}

	// Create and save a workflow
	workflowDef, err := workflow.NewDefinition(
		[]workflow.Place{"start"},
		[]workflow.Transition{},
	)
	if err != nil {
		t.Fatalf("Failed to create workflow definition: %v", err)
	}

	registry := workflow.NewRegistry()
	workflowMgr := workflow.NewManager(registry, sqlStore)

	wf, err := workflowMgr.CreateWorkflow(context.Background(), "old-wf", workflowDef, "start")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	wf.SetContext("title", "Old Document")
	wf.SetContext("content", "Old Content")

	if err := workflowMgr.SaveWorkflow(context.Background(), "old-wf", wf); err != nil {
		t.Fatalf("Failed to save workflow: %v", err)
	}

	// Step 2: Run migrations (simulating upgrade)
	if err := main.RunMigrations(db); err != nil {
		t.Fatalf("Migration failed: %v", err)
	}

	// Step 3: Reconfigure storage with new fields
	sqlStoreNew, err := storage.NewSQLiteStorage(db,
		storage.WithTable("workflow_states"),
		storage.WithCustomFields(map[string]string{
			"title":      "title TEXT",
			"content":    "content TEXT",
			"created_at": "created_at DATETIME",
			"updated_at": "updated_at DATETIME",
			"priority":   "priority INTEGER",
			"tags":       "tags TEXT",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create new storage: %v", err)
	}

	// Step 4: Verify old workflow still loads correctly
	loadedPlaces, loadedCtx, err := sqlStoreNew.LoadState(context.Background(), "old-wf")
	if err != nil {
		t.Fatalf("Failed to load old workflow: %v", err)
	}

	if p := loadedPlaces.Places(); len(p) != 1 || p[0] != "start" {
		t.Errorf("Expected places [start], got %v", loadedPlaces)
	}

	// Old fields should still be present
	if loadedCtx["title"] != "Old Document" {
		t.Errorf("Expected title 'Old Document', got %v", loadedCtx["title"])
	}
	if loadedCtx["content"] != "Old Content" {
		t.Errorf("Expected content 'Old Content', got %v", loadedCtx["content"])
	}

	// New fields should have default values (priority has DEFAULT 0 in migration)
	// Note: SQLite sets DEFAULT values for existing rows when adding columns
	if loadedCtx["priority"] != int64(0) {
		t.Errorf("Expected priority to be 0 (default) for old workflow, got %v", loadedCtx["priority"])
	}
	// Timestamp fields should be NULL since they don't have defaults
	if loadedCtx["created_at"] != nil {
		t.Errorf("Expected created_at to be nil for old workflow, got %v", loadedCtx["created_at"])
	}

	// Step 5: Update workflow with new fields
	wfNew, err := workflowMgr.GetWorkflow(context.Background(), "old-wf", workflowDef)
	if err != nil {
		t.Fatalf("Failed to get workflow: %v", err)
	}

	wfNew.SetContext("priority", 5)
	wfNew.SetContext("tags", "migrated")
	wfNew.SetContext("updated_at", time.Now().Format(time.RFC3339))

	workflowMgrNew := workflow.NewManager(registry, sqlStoreNew)
	if err := workflowMgrNew.SaveWorkflow(context.Background(), "old-wf", wfNew); err != nil {
		t.Fatalf("Failed to save workflow with new fields: %v", err)
	}

	// Step 6: Verify new fields are saved
	_, loadedCtx, err = sqlStoreNew.LoadState(context.Background(), "old-wf")
	if err != nil {
		t.Fatalf("Failed to load workflow after update: %v", err)
	}

	if loadedCtx["priority"] != int64(5) {
		t.Errorf("Expected priority 5, got %v", loadedCtx["priority"])
	}
	if loadedCtx["tags"] != "migrated" {
		t.Errorf("Expected tags 'migrated', got %v", loadedCtx["tags"])
	}

	t.Log("Backward compatibility test passed: old workflows work with new schema")
}
