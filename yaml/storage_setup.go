package yaml

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
)

// Connection represents a database connection that can be any type (SQL, NoSQL, etc.).
// The Underlying() method returns the concrete connection object.
// This allows the system to work with any database type without being tied to SQL.
//
// Example implementations:
//   - SQLConnection: wraps *sql.DB for SQL databases (sqlite, postgres, cockroachdb, etc.)
//   - MongoDBConnection: wraps *mongo.Client for MongoDB
//   - RedisConnection: wraps *redis.Client for Redis
//   - DynamoDBConnection: wraps *dynamodb.Client for AWS DynamoDB
type Connection interface {
	// Underlying returns the underlying database connection object.
	// For SQL databases, this returns *sql.DB.
	// For NoSQL databases, this returns the appropriate client (e.g., *mongo.Client, *redis.Client).
	// Returns nil if no connection is needed or available.
	Underlying() any
}

// SQLConnection wraps a SQL database connection (*sql.DB).
// Use this for SQL databases like SQLite, PostgreSQL, MySQL, CockroachDB, etc.
type SQLConnection struct {
	db *sql.DB
}

// DB returns the underlying *sql.DB connection.
// This provides type-safe access to the database connection.
func (c *SQLConnection) DB() *sql.DB {
	return c.db
}

// Underlying returns the underlying *sql.DB connection.
// This implements the Connection interface for polymorphic use.
func (c *SQLConnection) Underlying() any {
	return c.db
}

// StorageSetupResult contains all the initialized components from storage configuration.
type StorageSetupResult struct {
	Storage      workflow.Storage
	HistoryStore history.HistoryStore
	Connection   Connection // Generic connection interface - can be SQL, MongoDB, Redis, etc.
}

// SetupStorageFromConfig is a comprehensive helper that:
// 1. Sets up storage builders based on config
// 2. Builds and initializes storage
// 3. Sets up history store (if configured)
// 4. Returns everything ready to use
//
// Supports both SQL and NoSQL storage types:
//   - SQL (sqlite, postgres): Initializes schema, sets up DB connection, supports history store
//   - NoSQL (mongodb, redis): Uses custom builders, no SQL schema initialization
func SetupStorageFromConfig(storageConfig *StorageConfig) (*StorageSetupResult, error) {
	if storageConfig == nil {
		return nil, fmt.Errorf("storage config is required")
	}

	// Setup storage builders (returns connectionProvider for supported types, nil for others)
	connectionProvider, err := SetupStorageBuildersFromConfig(storageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to setup storage builders: %w", err)
	}

	// Get database connection (generic interface - can be SQL, MongoDB, Redis, etc.)
	var conn Connection
	if connectionProvider != nil {
		conn, err = connectionProvider()
		if err != nil {
			return nil, fmt.Errorf("failed to get database connection: %w", err)
		}
	}

	// Build storage
	workflowStore, storageInit, err := BuildStorage(storageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build storage: %w", err)
	}

	// Initialize storage schema
	if storageInit != nil {
		if storageInit.Schema != "" && conn != nil {
			// For SQL databases, execute schema
			if sqlConn, ok := conn.(*SQLConnection); ok && sqlConn.DB() != nil {
				log.Printf("Initializing storage schema:\n%s\n", storageInit.Schema)
				if _, err := sqlConn.DB().Exec(storageInit.Schema); err != nil {
					return nil, fmt.Errorf("failed to initialize storage schema: %w", err)
				}
			}
		}
		if storageInit.InitFunc != nil {
			if err := storageInit.InitFunc(); err != nil {
				return nil, fmt.Errorf("failed to run storage init function: %w", err)
			}
		}
	}

	// Setup history store (if history config is present and we have SQL connection)
	var historyStore history.HistoryStore
	if conn != nil {
		if sqlConn, ok := conn.(*SQLConnection); ok && sqlConn.DB() != nil {
			if historyConfig, ok := storageConfig.Config["history"].(map[string]any); ok {
				// Extract history configuration
				table := "transition_history"
				if tableName, ok := historyConfig["table"].(string); ok && tableName != "" {
					table = tableName
				}

				// Extract custom fields for history
				customFields := make(map[string]string)
				if customFieldsRaw, ok := historyConfig["custom_fields"].(map[string]any); ok {
					for k, v := range customFieldsRaw {
						if str, ok := v.(string); ok {
							customFields[k] = str
						}
					}
				}

				// Create history store (SQL-based)
				historyStore = history.NewSQLiteHistory(sqlConn.DB(),
					history.WithTable(table),
					history.WithCustomFields(customFields),
				)

				// Initialize history schema. This runs once at startup, so a
				// background context is sufficient here.
				if err := historyStore.Initialize(context.Background()); err != nil {
					return nil, fmt.Errorf("failed to initialize history store: %w", err)
				}
				log.Printf("History store initialized with table: %s", table)
			}
		}
		// For NoSQL databases, history store setup would be handled by custom builders
		// that implement their own history store types
	}

	return &StorageSetupResult{
		Storage:      workflowStore,
		HistoryStore: historyStore,
		Connection:   conn, // Generic connection - can be SQL, MongoDB, Redis, etc.
	}, nil
}

// SetupStorageBuilders registers storage builders based on the storage configuration.
// This allows the application to automatically register the required builders
// without explicitly knowing which storage type is being used.
//
// Currently supports:
//   - "sqlite": Registers SQLiteStorageBuilder
//
// If dbProvider is provided, it will be used for SQLite connections.
// If nil, SQLite builder will create connections from config.
func SetupStorageBuilders(storageConfig *StorageConfig, dbProvider func() (*sql.DB, error)) error {
	if storageConfig == nil {
		return nil // No storage config, nothing to register
	}

	switch storageConfig.Type {
	case "sqlite":
		// Register SQLite builder
		builder := NewSQLiteStorageBuilder(dbProvider)
		RegisterStorageBuilder(builder)
		return nil
	default:
		// Unknown storage type - don't register anything
		// This allows custom builders to be registered manually
		return fmt.Errorf("unknown storage type: %s (you may need to register a custom builder)", storageConfig.Type)
	}
}

// ConnectionProvider is a function that provides a Connection interface.
// This allows different storage types to return their appropriate connection type.
type ConnectionProvider func() (Connection, error)

// SetupStorageBuildersFromConfig automatically sets up storage builders based on the YAML config.
// It registers the appropriate builders for the storage type.
//
// For SQL databases (sqlite, postgres, cockroachdb, etc.):
//   - Returns a ConnectionProvider that returns a SQLConnection wrapping *sql.DB
//   - Registers the SQL-specific builder
//
// For NoSQL databases (mongodb, redis, dynamodb, etc.):
//   - Returns nil (custom builders should be registered manually)
//   - Custom builders can implement their own Connection types
//
// Returns:
//   - connectionProvider: For supported databases, a function returning Connection. For others, nil.
//   - error: If storage type setup fails
func SetupStorageBuildersFromConfig(storageConfig *StorageConfig) (ConnectionProvider, error) {
	if storageConfig == nil {
		return nil, fmt.Errorf("storage config is required")
	}

	switch storageConfig.Type {
	case "sqlite":
		// Extract database path from config
		dbPath := ":memory:" // default
		if dbPathConfig, ok := storageConfig.Config["database"].(string); ok && dbPathConfig != "" {
			dbPath = dbPathConfig
		}

		// Create dbProvider that opens the database
		dbProvider := func() (*sql.DB, error) {
			return sql.Open("sqlite3", dbPath)
		}

		// Register SQLite builder
		builder := NewSQLiteStorageBuilder(dbProvider)
		RegisterStorageBuilder(builder)

		// Return ConnectionProvider that wraps *sql.DB in SQLConnection
		connectionProvider := func() (Connection, error) {
			db, err := dbProvider()
			if err != nil {
				return nil, err
			}
			return &SQLConnection{db: db}, nil
		}

		return connectionProvider, nil
	default:
		// For NoSQL or unknown types, return nil (no connection provider)
		// Custom builders should be registered manually and can implement their own Connection types
		return nil, nil
	}
}
