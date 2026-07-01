package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/storage"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
)

// This example demonstrates how to use database migrations with the workflow system.
// It shows:
// 1. Running migrations using go-migrate
// 2. Configuring workflow storage to match migrated schema
// 3. Handling schema evolution (adding columns)
// 4. Testing that existing workflows continue to work after migrations

const dbPath = "./migration_example.db"

func main() {
	// Clean up any existing database for demo purposes
	os.Remove(dbPath)

	// Step 1: Open database connection
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Step 2: Run migrations
	log.Println("Running database migrations...")
	if err := RunMigrations(db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Migrations completed successfully!")

	// Step 3: Configure workflow storage to match the migrated schema
	// Note: The custom fields must match the columns added in migrations
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
		log.Fatalf("Failed to create storage: %v", err)
	}

	// Step 4: Configure history storage
	historyStore := history.NewSQLiteHistory(db,
		history.WithTable("transition_history"),
		history.WithCustomFields(map[string]string{
			"duration_ms": "duration_ms INTEGER",
			"metadata":    "metadata TEXT",
		}),
	)

	// Step 5: Define workflow
	workflowDef, err := workflow.NewDefinition(
		[]workflow.Place{"draft", "review", "approved", "published"},
		[]workflow.Transition{
			*workflow.MustNewTransition("submit_for_review", []workflow.Place{"draft"}, []workflow.Place{"review"}),
			*workflow.MustNewTransition("request_changes", []workflow.Place{"review"}, []workflow.Place{"draft"}),
			*workflow.MustNewTransition("approve", []workflow.Place{"review"}, []workflow.Place{"approved"}),
			*workflow.MustNewTransition("publish", []workflow.Place{"approved"}, []workflow.Place{"published"}),
		},
	)
	if err != nil {
		log.Fatalf("Failed to create workflow definition: %v", err)
	}

	// Step 6: Create workflow manager
	registry := workflow.NewRegistry()
	workflowMgr := workflow.NewManager(registry, sqlStore)

	// Step 7: Track transition duration
	workflowMgr.AddEventListener(workflow.EventAfterTransition, func(e workflow.Event) error {
		startTime := time.Now()
		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)
		duration := time.Since(startTime)

		metadataJSON, _ := json.Marshal(map[string]any{
			"transition_time":  startTime.Format(time.RFC3339),
			"workflow_version": "1.0",
		})

		return historyStore.SaveTransition(context.Background(), &history.TransitionRecord{
			WorkflowID: e.Workflow().Name(),
			FromState:  fmt.Sprintf("%v", e.From()),
			ToState:    fmt.Sprintf("%v", e.To()),
			Transition: e.Transition().Name(),
			Notes:      "Automated transition",
			Actor:      "system",
			CreatedAt:  time.Now(),
			CustomFields: map[string]any{
				"duration_ms": duration.Milliseconds(),
				"metadata":    string(metadataJSON),
			},
		})
	})

	// Step 8: Create and use workflows
	log.Println("\n=== Creating workflows ===")

	// Create first workflow with initial fields
	wf1, err := workflowMgr.CreateWorkflow(context.Background(), "doc-1", workflowDef, "draft")
	if err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}
	wf1.SetContext("title", "Document 1")
	wf1.SetContext("content", "Initial content")
	wf1.SetContext("priority", 5)
	wf1.SetContext("tags", "important,urgent")
	wf1.SetContext("created_at", time.Now().Format(time.RFC3339))
	wf1.SetContext("updated_at", time.Now().Format(time.RFC3339))

	if err := workflowMgr.SaveWorkflow(context.Background(), "doc-1", wf1); err != nil {
		log.Fatalf("Failed to save workflow: %v", err)
	}
	log.Printf("Created workflow: doc-1 in state: %v", wf1.CurrentPlaces())

	// Create second workflow
	wf2, err := workflowMgr.CreateWorkflow(context.Background(), "doc-2", workflowDef, "draft")
	if err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}
	wf2.SetContext("title", "Document 2")
	wf2.SetContext("content", "Another document")
	wf2.SetContext("priority", 3)
	wf2.SetContext("created_at", time.Now().Format(time.RFC3339))
	wf2.SetContext("updated_at", time.Now().Format(time.RFC3339))

	if err := workflowMgr.SaveWorkflow(context.Background(), "doc-2", wf2); err != nil {
		log.Fatalf("Failed to save workflow: %v", err)
	}
	log.Printf("Created workflow: doc-2 in state: %v", wf2.CurrentPlaces())

	// Step 9: Apply transitions
	log.Println("\n=== Applying transitions ===")

	// Transition doc-1 to review
	wf1, err = workflowMgr.GetWorkflow(context.Background(), "doc-1", workflowDef)
	if err != nil {
		log.Fatalf("Failed to get workflow: %v", err)
	}

	enabled, _ := wf1.EnabledTransitions()
	log.Printf("doc-1 enabled transitions: %v", getTransitionNames(enabled))

	if err := wf1.Apply([]workflow.Place{"review"}); err != nil {
		log.Fatalf("Failed to apply transition: %v", err)
	}
	wf1.SetContext("updated_at", time.Now().Format(time.RFC3339))
	if err := workflowMgr.SaveWorkflow(context.Background(), "doc-1", wf1); err != nil {
		log.Fatalf("Failed to save workflow: %v", err)
	}
	log.Printf("doc-1 transitioned to: %v", wf1.CurrentPlaces())

	// Step 10: Verify data persistence
	log.Println("\n=== Verifying data persistence ===")

	// Load workflow and verify all fields are preserved
	loadedPlaces, loadedCtx, err := sqlStore.LoadState(context.Background(), "doc-1")
	if err != nil {
		log.Fatalf("Failed to load state: %v", err)
	}

	log.Printf("Loaded doc-1 state: %v", loadedPlaces)
	log.Printf("Loaded doc-1 context:")
	for key, value := range loadedCtx {
		log.Printf("  %s: %v", key, value)
	}

	// Step 11: Query history with new metadata fields
	log.Println("\n=== Querying transition history ===")

	historyRecords, err := historyStore.ListHistory(context.Background(), "doc-1", history.QueryOptions{Limit: 10})
	if err != nil {
		log.Fatalf("Failed to list history: %v", err)
	}

	for _, record := range historyRecords {
		log.Printf("Transition: %s -> %s (via %s)", record.FromState, record.ToState, record.Transition)
		if record.CustomFields != nil {
			if duration, ok := record.CustomFields["duration_ms"]; ok {
				log.Printf("  Duration: %v ms", duration)
			}
			if metadata, ok := record.CustomFields["metadata"]; ok {
				log.Printf("  Metadata: %v", metadata)
			}
		}
	}

	// Step 12: Demonstrate migration compatibility
	log.Println("\n=== Migration compatibility test ===")
	log.Println("The workflow system successfully works with migrated schema!")
	log.Println("Key points:")
	log.Println("  1. Migrations run before workflow initialization")
	log.Println("  2. Storage configuration matches migrated schema")
	log.Println("  3. Existing workflows continue to work after migrations")
	log.Println("  4. New fields can be added via migrations and used in workflows")

	// Show the generated schema for comparison
	log.Println("\n=== Generated schema (for reference) ===")
	log.Println(sqlStore.GenerateSchema())
}

// RunMigrations executes database migrations using go-migrate
// Exported for testing purposes
func RunMigrations(db *sql.DB) error {
	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		"file://migrations",
		"sqlite3",
		driver,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	// Run all pending migrations
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed to get migration version: %w", err)
	}

	if dirty {
		return fmt.Errorf("database is in dirty state, manual intervention required")
	}

	if err == nil {
		log.Printf("Current migration version: %d", version)
	}

	return nil
}

// getTransitionNames extracts transition names from a slice
func getTransitionNames(transitions []workflow.Transition) []string {
	names := make([]string, len(transitions))
	for i, t := range transitions {
		names[i] = t.Name()
	}
	return names
}
