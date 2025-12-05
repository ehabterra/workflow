package storage_test

import (
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
	context := map[string]interface{}{"foo": "bar"}
	if err := s.SaveState("wf1", places, context); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	loadedPlaces, loadedContext, err := s.LoadState("wf1")
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
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
	context := map[string]interface{}{
		"ip_address": "127.0.0.1",
		"user_agent": "test-agent",
	}
	if err := s.SaveState("wf2", places, context); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	_, loadedContext, err := s.LoadState("wf2")
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
	context := map[string]interface{}{}
	if err := s.SaveState("wf3", places, context); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}
	if err := s.DeleteState("wf3"); err != nil {
		t.Fatalf("failed to delete state: %v", err)
	}
	_, _, err = s.LoadState("wf3")
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
	err = s.SaveState("empty_places", []workflow.Place{}, nil)
	if err != nil {
		t.Errorf("SaveState() with empty places failed: %v", err)
	}

	// Test saving with nil context
	err = s.SaveState("nil_context", []workflow.Place{"draft"}, nil)
	if err != nil {
		t.Errorf("SaveState() with nil context failed: %v", err)
	}

	// Test saving with empty context
	err = s.SaveState("empty_context", []workflow.Place{"draft"}, map[string]interface{}{})
	if err != nil {
		t.Errorf("SaveState() with empty context failed: %v", err)
	}

	// Test loading non-existent workflow
	_, _, err = s.LoadState("non_existent")
	if err == nil {
		t.Error("LoadState() for non-existent workflow should return error")
	}

	// Test deleting non-existent workflow (should not error)
	err = s.DeleteState("non_existent")
	if err != nil {
		t.Errorf("DeleteState() for non-existent workflow should not error: %v", err)
	}

	// Test saving with multiple places
	err = s.SaveState("multiple_places", []workflow.Place{"place1", "place2", "place3"}, nil)
	if err != nil {
		t.Errorf("SaveState() with multiple places failed: %v", err)
	}

	places, _, err := s.LoadState("multiple_places")
	if err != nil {
		t.Fatalf("LoadState() failed: %v", err)
	}
	if len(places) != 3 {
		t.Errorf("LoadState() places count = %d, want 3", len(places))
	}

	// Test saving with various context value types
	complexContext := map[string]interface{}{
		"string": "value",
		"int":    42,
		"float":  3.14,
		"bool":   true,
		"nil":    nil,
		"array":  []interface{}{1, 2, 3},
		"nested": map[string]interface{}{"key": "value"},
	}
	err = s.SaveState("complex_context", []workflow.Place{"draft"}, complexContext)
	if err != nil {
		t.Errorf("SaveState() with complex context failed: %v", err)
	}

	_, loadedContext, err := s.LoadState("complex_context")
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
	context := map[string]interface{}{"key": "value"}
	if err := s.SaveState("wf_custom", places, context); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	loadedPlaces, loadedContext, err := s.LoadState("wf_custom")
	if err != nil {
		t.Fatalf("failed to load state: %v", err)
	}
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
	context := map[string]interface{}{
		"field1": "text_value",
		"field2": 42,
		"field3": 3.14,
	}
	if err := s.SaveState("wf_custom_fields", []workflow.Place{"draft"}, context); err != nil {
		t.Fatalf("failed to save state: %v", err)
	}

	_, loadedContext, err := s.LoadState("wf_custom_fields")
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
