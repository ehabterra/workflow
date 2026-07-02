package yaml_test

import (
	"database/sql"
	"testing"

	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

func TestStorageFactory(t *testing.T) {
	factory := yaml.NewStorageFactory()

	// Register SQLite builder
	dbProvider := func() (*sql.DB, error) {
		return sql.Open("sqlite3", ":memory:")
	}
	builder := yaml.NewSQLiteStorageBuilder(dbProvider)
	factory.Register(builder)

	// Test registered types
	types := factory.RegisteredTypes()
	if len(types) != 1 {
		t.Errorf("Expected 1 registered type, got %d", len(types))
	}
	if types[0] != "sqlite" {
		t.Errorf("Expected type 'sqlite', got '%s'", types[0])
	}

	// Test building storage
	config := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"table":        "test_workflows",
			"id_column":    "id",
			"state_column": "state",
			"custom_fields": map[string]any{
				"title": "title TEXT",
			},
		},
	}

	store, init, err := factory.Build(config)
	if err != nil {
		t.Fatalf("Failed to build storage: %v", err)
	}

	if store == nil {
		t.Fatal("Storage is nil")
	}

	if init == nil {
		t.Fatal("StorageInit is nil")
	}

	if init.Schema == "" {
		t.Error("Schema should not be empty")
	}

	// Test initialization
	if init.InitFunc != nil {
		if err := init.InitFunc(); err != nil {
			t.Errorf("Failed to initialize storage: %v", err)
		}
	}
}

func TestStorageFactory_UnknownType(t *testing.T) {
	factory := yaml.NewStorageFactory()

	config := &yaml.StorageConfig{
		Type: "unknown",
	}

	_, _, err := factory.Build(config)
	if err == nil {
		t.Error("Expected error for unknown storage type")
	}
}

func TestDefaultFactory(t *testing.T) {
	// Register with default factory
	dbProvider := func() (*sql.DB, error) {
		return sql.Open("sqlite3", ":memory:")
	}
	builder := yaml.NewSQLiteStorageBuilder(dbProvider)
	yaml.RegisterStorageBuilder(builder)

	// Test building with default factory
	config := &yaml.StorageConfig{
		Type: "sqlite",
		Config: map[string]any{
			"table": "test_workflows",
		},
	}

	store, init, err := yaml.BuildStorage(config)
	if err != nil {
		t.Fatalf("Failed to build storage: %v", err)
	}

	if store == nil {
		t.Fatal("Storage is nil")
	}

	if init == nil {
		t.Fatal("StorageInit is nil")
	}
}
