# Database Migration Example

This example demonstrates how to use database migrations with the workflow system using [go-migrate](https://github.com/golang-migrate/migrate), a popular migration tool for Go.

## Overview

This example shows:

- How to set up and run database migrations before initializing workflows
- How to handle schema evolution (adding columns) while preserving existing data
- How to configure workflow storage to match migrated schema
- How to test migration compatibility and backward compatibility
- Best practices for managing database schema changes in production

## Prerequisites

- Go 1.21 or later
- SQLite3

## Installation

1. Navigate to the example directory:

    ```bash
    cd examples/migration_example
    ```

1. Install dependencies:

    ```bash
    go mod download
    ```

## Running the Example

Run the example:

```bash
go run main.go
```

This will:

1. Create a new SQLite database
2. Run all migrations in order
3. Create workflows with fields from all migrations
4. Demonstrate that workflows work correctly with the migrated schema

## Running Tests

Run the test suite:

```bash
go test -v
```

The tests verify:

- Workflows work correctly with migrated schema
- History tracking works with migrated fields
- Backward compatibility (old workflows work after migrations)

## Migration Files

The migrations are located in the `migrations/` directory:

### 000001_initial_schema.up.sql / .down.sql

Creates the initial schema:

- `workflow_states` table with basic fields (id, state, title, content)
- `transition_history` table with basic fields
- Indexes for performance

### 000002_add_metadata.up.sql / .down.sql

Adds metadata columns to `workflow_states`:

- `created_at` - timestamp when workflow was created
- `updated_at` - timestamp when workflow was last updated
- `priority` - integer priority level
- `tags` - text tags for categorization

### 000003_add_history_metadata.up.sql / .down.sql

Adds metadata to `transition_history`:

- `duration_ms` - transition duration in milliseconds
- `metadata` - JSON metadata for transitions

## How It Works

### 1. Migration Execution

Migrations are run before workflow initialization:

```go
// Open database
db, err := sql.Open("sqlite3", "./migration_example.db")

// Run migrations
if err := runMigrations(db); err != nil {
    log.Fatalf("Migration failed: %v", err)
}
```

### 2. Storage Configuration

After migrations, configure storage to match the migrated schema:

```go
sqlStore, err := storage.NewSQLiteStorage(db,
    storage.WithTable("workflow_states"),
    storage.WithCustomFields(map[string]string{
        "title":      "title TEXT",
        "content":    "content TEXT",
        "created_at": "created_at DATETIME",  // Added in migration 2
        "updated_at": "updated_at DATETIME",  // Added in migration 2
        "priority":   "priority INTEGER",     // Added in migration 2
        "tags":       "tags TEXT",            // Added in migration 2
    }),
)
```

**Important**: The custom fields in `WithCustomFields()` must match the columns created by migrations.

### 3. Using Migrated Fields

Once configured, you can use the new fields in your workflows:

```go
wf.SetContext("priority", 5)
wf.SetContext("tags", "important,urgent")
wf.SetContext("created_at", time.Now().Format(time.RFC3339))
wf.SetContext("updated_at", time.Now().Format(time.RFC3339))
```

## Best Practices

### 1. Migration Order

- Always run migrations before initializing workflow storage
- Migrations should be idempotent (safe to run multiple times)
- Use `IF NOT EXISTS` for table creation
- Use `ADD COLUMN IF NOT EXISTS` when supported (SQLite has limitations)

### 2. Schema Evolution

When adding new columns:

- Add columns with `DEFAULT` values to avoid breaking existing data
- Update storage configuration to include new fields
- Test that existing workflows continue to work
- Consider data migration scripts for complex changes

### 3. SQLite Limitations

SQLite has some limitations compared to other databases:

- `ALTER TABLE ADD COLUMN` is supported
- `ALTER TABLE DROP COLUMN` is NOT supported (requires table recreation)
- For production, consider PostgreSQL or MySQL for better migration support

### 4. Testing Migrations

Always test:

- Forward migrations (up)
- Backward migrations (down) - when possible
- Data preservation during migrations
- Backward compatibility with existing workflows

### 5. Production Deployment

For production:

1. Test migrations on a copy of production data
2. Backup database before running migrations
3. Run migrations during maintenance windows for large changes
4. Monitor application after migration
5. Have a rollback plan

## Migration Workflow

```sh
┌─────────────────┐
│  Start App      │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  Open Database  │
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Run Migrations  │ ◄─── Check migration version
└────────┬────────┘      Apply pending migrations
         │
         ▼
┌─────────────────┐
│ Configure       │
│ Storage         │ ◄─── Match migrated schema
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│ Initialize      │
│ Workflows       │
└─────────────────┘
```

## Common Patterns

### Adding a New Field

1. Create migration file:

  ```sql
  -- migrations/000004_add_status.up.sql
  ALTER TABLE workflow_states ADD COLUMN status TEXT DEFAULT 'active';
  ```

1. Update storage configuration:

  ```go
  storage.WithCustomFields(map[string]string{
      // ... existing fields
      "status": "status TEXT",  // Add new field
  })
  ```

1. Use in workflows:

  ```go
  wf.SetContext("status", "active")
  ```

### Handling Data Migration

For complex data migrations, use a separate migration step:

```sql
-- migrations/000005_migrate_data.up.sql
-- Update existing rows with default values
UPDATE workflow_states 
SET status = 'active' 
WHERE status IS NULL;
```

## Troubleshooting

### Migration Fails

If a migration fails:

1. Check the error message
2. Verify SQL syntax
3. Check if database is in "dirty" state
4. Manually fix and mark migration as complete if needed

### Schema Mismatch

If storage configuration doesn't match schema:

- Error: `no such column: X`
- Solution: Update `WithCustomFields()` to match migrated schema

### Backward Compatibility Issues

If old workflows break after migration:

- Ensure new columns have DEFAULT values
- Test with existing data before deploying
- Consider data migration scripts

This demonstrates that the workflow system integrates seamlessly with database migrations.
