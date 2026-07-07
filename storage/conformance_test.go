package storage_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	"github.com/ehabterra/workflow/storagetest"
	_ "github.com/mattn/go-sqlite3"
)

// TestSQLiteConformance runs the shared Storage conformance suite (including the
// versioning contract) against the SQLite backend.
func TestSQLiteConformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) workflow.Storage {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		// A plain :memory: database exists per connection, so cap the pool at
		// one conn — otherwise a concurrent subtest's second connection sees
		// an empty database. Concurrency still interleaves at the pool.
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = db.Close() })

		store, err := storage.NewSQLiteStorage(db)
		if err != nil {
			t.Fatalf("NewSQLiteStorage: %v", err)
		}
		// EnsureSchema (rather than Initialize) so the conformance suite also runs
		// against the M4 due column and its index.
		if err := store.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		return store
	})
}
