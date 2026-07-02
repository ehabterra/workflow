package storage_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	return db
}

func TestSQLiteStorage_Basic(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db, storage.WithCustomFields(map[string]string{
		"foo": "foo TEXT",
	}))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	if err := storage.Initialize(db, s.GenerateSchema()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	places := []workflow.Place{"draft"}
	contextData := map[string]any{"foo": "bar"}
	if err := s.SaveState(context.Background(), "wf1", workflow.NewMarking(places), contextData); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	loadedMarking, loadedContext, err := s.LoadState(context.Background(), "wf1")
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	loadedPlaces := loadedMarking.Places()
	if len(loadedPlaces) != 1 || loadedPlaces[0] != "draft" {
		t.Errorf("unexpected places: %+v", loadedPlaces)
	}
	if loadedContext["foo"] != "bar" {
		t.Errorf("unexpected context: %+v", loadedContext)
	}
}

func TestSQLiteStorage_CustomFields(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db, storage.WithCustomFields(map[string]string{
		"ip_address": "ip_address TEXT",
		"user_agent": "user_agent TEXT",
	}))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	if err := storage.Initialize(db, s.GenerateSchema()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	places := []workflow.Place{"review"}
	contextData := map[string]any{
		"ip_address": "127.0.0.1",
		"user_agent": "test-agent",
	}
	if err := s.SaveState(context.Background(), "wf2", workflow.NewMarking(places), contextData); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	_, loadedContext, err := s.LoadState(context.Background(), "wf2")
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	if loadedContext["ip_address"] != "127.0.0.1" || loadedContext["user_agent"] != "test-agent" {
		t.Errorf("unexpected custom fields: %+v", loadedContext)
	}
}

func TestSQLiteStorage_DeleteState(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	if err := storage.Initialize(db, s.GenerateSchema()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}
	places := []workflow.Place{"draft"}
	contextData := map[string]any{}
	if err := s.SaveState(context.Background(), "wf3", workflow.NewMarking(places), contextData); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}
	if err := s.DeleteState(context.Background(), "wf3"); err != nil {
		t.Fatalf("failed to delete state: %v", err)
	}
	_, _, err = s.LoadState(context.Background(), "wf3")
	if err == nil {
		t.Errorf("expected error when loading deleted state")
	}
}

func TestSQLiteStorage_EdgeCases(t *testing.T) {
	db := setupTestDB(t)
	// Configure custom fields for the complex context test
	s, err := storage.NewSQLiteStorage(db, storage.WithCustomFields(map[string]string{
		"string": "string TEXT",
		"int":    "int INTEGER",
		"float":  "float REAL",
		"bool":   "bool INTEGER",
		"nil":    "nil TEXT",
		"array":  "array TEXT",
		"nested": "nested TEXT",
	}))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	if err := storage.Initialize(db, s.GenerateSchema()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// Test saving with empty places
	err = s.SaveState(context.Background(), "empty_places", workflow.NewMarking(nil), nil)
	if err != nil {
		t.Errorf("SaveState() with empty places failed: %v", err)
	}

	// Test saving with nil context
	err = s.SaveState(context.Background(), "nil_context", workflow.NewMarking([]workflow.Place{"draft"}), nil)
	if err != nil {
		t.Errorf("SaveState() with nil context failed: %v", err)
	}

	// Test saving with empty context
	err = s.SaveState(context.Background(), "empty_context", workflow.NewMarking([]workflow.Place{"draft"}), map[string]any{})
	if err != nil {
		t.Errorf("SaveState() with empty context failed: %v", err)
	}

	// Test loading non-existent workflow
	_, _, err = s.LoadState(context.Background(), "non_existent")
	if err == nil {
		t.Error("LoadState() for non-existent workflow should return error")
	}

	// Test deleting non-existent workflow (should not error)
	err = s.DeleteState(context.Background(), "non_existent")
	if err != nil {
		t.Errorf("DeleteState() for non-existent workflow should not error: %v", err)
	}

	// Test saving with multiple places
	err = s.SaveState(context.Background(), "multiple_places", workflow.NewMarking([]workflow.Place{"place1", "place2", "place3"}), nil)
	if err != nil {
		t.Errorf("SaveState() with multiple places failed: %v", err)
	}

	m, _, err := s.LoadState(context.Background(), "multiple_places")
	if err != nil {
		t.Fatalf("LoadState() failed: %v", err)
	}
	places := m.Places()
	if len(places) != 3 {
		t.Errorf("LoadState() places count = %d, want 3", len(places))
	}

	// Test saving with various context value types
	complexContext := map[string]any{
		"string": "value",
		"int":    42,
		"float":  3.14,
		"bool":   true,
		"nil":    nil,
		"array":  []any{1, 2, 3},
		"nested": map[string]any{"key": "value"},
	}
	err = s.SaveState(context.Background(), "complex_context", workflow.NewMarking([]workflow.Place{"draft"}), complexContext)
	if err != nil {
		t.Errorf("SaveState() with complex context failed: %v", err)
	}

	_, loadedContext, err := s.LoadState(context.Background(), "complex_context")
	if err != nil {
		t.Fatalf("LoadState() failed: %v", err)
	}

	// Verify some context values (JSON unmarshaling may change types)
	// Note: bool is stored as INTEGER in SQLite (1 for true, 0 for false)
	if loadedContext["string"] != "value" {
		t.Errorf("LoadState() string value = %v, want 'value'", loadedContext["string"])
	}
	// SQLite stores booleans as integers, so we need to check for int64(1) or true
	boolVal := loadedContext["bool"]
	if boolVal != true && boolVal != int64(1) {
		t.Errorf("LoadState() bool value = %v, want true or 1", boolVal)
	}
}

func TestSQLiteStorage_WithTable(t *testing.T) {
	db := setupTestDB(t)

	// Test with custom table name and custom field for context
	customTable := "custom_workflow_states"
	s, err := storage.NewSQLiteStorage(db, storage.WithTable(customTable), storage.WithCustomFields(map[string]string{
		"key": "key TEXT",
	}))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	// Verify table name is used in schema
	schema := s.GenerateSchema()
	if !contains(schema, customTable) {
		t.Errorf("Schema does not contain custom table name %q", customTable)
	}

	if err := storage.Initialize(db, schema); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// Test saving and loading with custom table
	places := []workflow.Place{"draft"}
	contextData := map[string]any{"key": "value"}
	if err := s.SaveState(context.Background(), "wf_custom", workflow.NewMarking(places), contextData); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	loadedMarking, loadedContext, err := s.LoadState(context.Background(), "wf_custom")
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	loadedPlaces := loadedMarking.Places()
	if len(loadedPlaces) != 1 || loadedPlaces[0] != "draft" {
		t.Errorf("unexpected places: %+v", loadedPlaces)
	}
	if loadedContext["key"] != "value" {
		t.Errorf("unexpected context: %+v", loadedContext)
	}
}

func TestSQLiteStorage_WithCustomFields(t *testing.T) {
	db := setupTestDB(t)

	// Test with multiple custom fields
	s, err := storage.NewSQLiteStorage(db, storage.WithCustomFields(map[string]string{
		"field1": "field1 TEXT",
		"field2": "field2 INTEGER",
		"field3": "field3 REAL",
	}))
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	schema := s.GenerateSchema()
	if !contains(schema, "field1") || !contains(schema, "field2") || !contains(schema, "field3") {
		t.Error("Schema does not contain all custom fields")
	}

	if err := storage.Initialize(db, schema); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// Test saving with custom fields
	contextData := map[string]any{
		"field1": "text_value",
		"field2": 42,
		"field3": 3.14,
	}
	if err := s.SaveState(context.Background(), "wf_custom_fields", workflow.NewMarking([]workflow.Place{"draft"}), contextData); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	_, loadedContext, err := s.LoadState(context.Background(), "wf_custom_fields")
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
	if loadedContext["field1"] != "text_value" {
		t.Errorf("field1 = %v, want 'text_value'", loadedContext["field1"])
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			strings.Contains(s, substr))))
}
