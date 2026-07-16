// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

// TestExampleYAML verifies that all properties in example.yaml are supported
func TestExampleYAML(t *testing.T) {
	// Load the example.yaml file
	config, err := yaml.LoadConfig("example.yaml")
	if err != nil {
		t.Fatalf("Failed to load example.yaml: %v", err)
	}

	// Verify workflow properties
	if config.Workflow.Name != "blog_publishing" {
		t.Errorf("Expected name 'blog_publishing', got '%s'", config.Workflow.Name)
	}

	if _, ok := config.Workflow.InitialMarking.Places["draft"]; !ok {
		t.Errorf("Expected initial_marking to include 'draft', got %v", config.Workflow.InitialMarking.Places)
	}

	// Verify metadata
	if config.Workflow.Metadata == nil {
		t.Error("Expected metadata to be set")
	} else {
		if config.Workflow.Metadata["title"] != "Blog Publishing Workflow" {
			t.Errorf("Expected title metadata, got %v", config.Workflow.Metadata["title"])
		}
	}

	// Verify places
	if len(config.Workflow.Places) != 4 {
		t.Errorf("Expected 4 places, got %d", len(config.Workflow.Places))
	}

	// Verify place metadata
	draftPlace := findPlace(config.Workflow.Places, "draft")
	if draftPlace == nil {
		t.Error("Expected 'draft' place")
	} else if draftPlace.Metadata["max_words"] != 500 {
		t.Errorf("Expected max_words=500, got %v", draftPlace.Metadata["max_words"])
	}

	// Verify transitions
	if len(config.Workflow.Transitions) != 3 {
		t.Errorf("Expected 3 transitions, got %d", len(config.Workflow.Transitions))
	}

	// Verify transition properties
	toReviewTrans := findTransition(config.Workflow.Transitions, "to_review")
	if toReviewTrans == nil {
		t.Error("Expected 'to_review' transition")
	} else {
		if len(toReviewTrans.From) != 1 || toReviewTrans.From[0] != "draft" {
			t.Errorf("Expected from=[draft], got %v", toReviewTrans.From)
		}
		if len(toReviewTrans.To) != 1 || toReviewTrans.To[0] != "reviewed" {
			t.Errorf("Expected to=[reviewed], got %v", toReviewTrans.To)
		}
		if toReviewTrans.Guard == "" {
			t.Error("Expected guard expression")
		}
		if toReviewTrans.Metadata == nil || toReviewTrans.Metadata["priority"] != 0.5 {
			t.Errorf("Expected metadata with priority=0.5, got %v", toReviewTrans.Metadata)
		}
		if toReviewTrans.Notes != "Submitted for review" {
			t.Errorf("Expected notes 'Submitted for review', got '%s'", toReviewTrans.Notes)
		}
		if toReviewTrans.Actor != "author" {
			t.Errorf("Expected actor 'author', got '%s'", toReviewTrans.Actor)
		}
		if toReviewTrans.CustomFields == nil || toReviewTrans.CustomFields["submission_method"] != "web" {
			t.Errorf("Expected custom_fields with submission_method='web', got %v", toReviewTrans.CustomFields)
		}
	}

	// Verify storage config
	if config.Storage == nil {
		t.Error("Expected storage config")
	} else {
		if config.Storage.Type != "sqlite" {
			t.Errorf("Expected storage type 'sqlite', got '%s'", config.Storage.Type)
		}

		// Check storage config fields
		storageConfig := config.Storage.Config
		if storageConfig["table"] != "workflow_states" {
			t.Errorf("Expected table='workflow_states', got '%v'", storageConfig["table"])
		}
		if storageConfig["id_column"] != "id" {
			t.Errorf("Expected id_column='id', got '%v'", storageConfig["id_column"])
		}
		if storageConfig["state_column"] != "state" {
			t.Errorf("Expected state_column='state', got '%v'", storageConfig["state_column"])
		}
		if storageConfig["database"] != ":memory:" {
			t.Errorf("Expected database=':memory:', got '%v'", storageConfig["database"])
		}

		// Check custom fields
		if customFields, ok := storageConfig["custom_fields"].(map[string]any); ok {
			if customFields["title"] != "title TEXT" {
				t.Errorf("Expected custom_fields.title='title TEXT', got '%v'", customFields["title"])
			}
		} else {
			t.Error("Expected custom_fields in storage config")
		}
	}

	// Test that we can actually load and use the workflow
	loader := yaml.NewLoader()
	wf, err := loader.LoadWorkflow(config, "test-workflow")
	if err != nil {
		t.Fatalf("Failed to load workflow: %v", err)
	}

	// Verify metadata is injected
	title, ok := wf.Context("title")
	if !ok || title != "Blog Publishing Workflow" {
		t.Errorf("Expected title in context, got %v", title)
	}

	// Verify place metadata is stored
	placeMeta, ok := wf.Context("_place_metadata")
	if !ok {
		t.Error("Expected _place_metadata in context")
	} else {
		metaMap := placeMeta.(map[string]map[string]any)
		if metaMap["draft"]["max_words"] != 500 {
			t.Errorf("Expected draft.max_words=500, got %v", metaMap["draft"]["max_words"])
		}
	}

	// Test storage builder
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Logf("Failed to close database: %v", err)
		}
	}()

	dbProvider := func() (*sql.DB, error) { return db, nil }
	builder := yaml.NewSQLiteStorageBuilder(dbProvider)
	yaml.RegisterStorageBuilder(builder)

	store, init, err := yaml.BuildStorage(config.Storage)
	if err != nil {
		t.Fatalf("Failed to build storage: %v", err)
	}

	if store == nil {
		t.Error("Expected storage instance")
	}

	if init == nil || init.Schema == "" {
		t.Error("Expected storage init with schema")
	}

	// Test history with template resolution
	historyStore := history.NewSQLiteHistory(db,
		history.WithCustomFields(map[string]string{
			"publish_time": "publish_time TEXT",
			"ip_address":   "ip_address TEXT",
		}),
	)
	if err := historyStore.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize history: %v", err)
	}

	// Verify workflow can be used
	_ = wf
	_ = store
	_ = historyStore
}

func findPlace(places []yaml.PlaceConfig, name string) *yaml.PlaceConfig {
	for i := range places {
		if places[i].Name == name {
			return &places[i]
		}
	}
	return nil
}

func findTransition(transitions []yaml.TransitionConfig, name string) *yaml.TransitionConfig {
	for i := range transitions {
		if transitions[i].Name == name {
			return &transitions[i]
		}
	}
	return nil
}
