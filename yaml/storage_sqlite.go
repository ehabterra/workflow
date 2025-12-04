package yaml

import (
	"database/sql"
	"fmt"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
)

// SQLiteStorageBuilder implements StorageConfigBuilder for SQLite storage.
type SQLiteStorageBuilder struct {
	// DBProvider is a function that provides a *sql.DB instance.
	// This allows the builder to work with existing database connections.
	DBProvider func() (*sql.DB, error)
}

// NewSQLiteStorageBuilder creates a new SQLite storage builder.
// dbProvider is a function that returns a *sql.DB instance.
// If nil, the builder will try to create a connection from config.
func NewSQLiteStorageBuilder(dbProvider func() (*sql.DB, error)) *SQLiteStorageBuilder {
	return &SQLiteStorageBuilder{
		DBProvider: dbProvider,
	}
}

// Type returns "sqlite".
func (b *SQLiteStorageBuilder) Type() string {
	return "sqlite"
}

// Build creates a SQLite storage instance from YAML configuration.
func (b *SQLiteStorageBuilder) Build(config map[string]any) (workflow.Storage, *StorageInit, error) {
	// Get database connection
	var db *sql.DB
	var err error

	if b.DBProvider != nil {
		db, err = b.DBProvider()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get database connection: %w", err)
		}
	} else {
		// Try to create connection from config
		database, _ := config["database"].(string)
		if database == "" {
			database = ":memory:"
		}

		db, err = sql.Open("sqlite3", database)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to open SQLite database: %w", err)
		}
	}

	// Build options
	opts := []storage.Option{}

	// Table name
	if table, ok := config["table"].(string); ok && table != "" {
		opts = append(opts, storage.WithTable(table))
	}

	// ID column
	if idColumn, ok := config["id_column"].(string); ok && idColumn != "" {
		opts = append(opts, storage.WithIDColumn(idColumn))
	}

	// State column
	if stateColumn, ok := config["state_column"].(string); ok && stateColumn != "" {
		opts = append(opts, storage.WithStateColumn(stateColumn))
	}

	// Custom fields
	if customFieldsRaw, ok := config["custom_fields"].(map[string]any); ok {
		customFields := make(map[string]string)
		for k, v := range customFieldsRaw {
			if str, ok := v.(string); ok {
				customFields[k] = str
			}
		}
		if len(customFields) > 0 {
			opts = append(opts, storage.WithCustomFields(customFields))
		}
	}

	// Create storage
	store, err := storage.NewSQLiteStorage(db, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create SQLite storage: %w", err)
	}

	// Generate schema
	schema := store.GenerateSchema()

	return store, &StorageInit{
		Schema: schema,
		InitFunc: func() error {
			return storage.Initialize(db, schema)
		},
	}, nil
}
