package storage_test

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	"github.com/ehabterra/workflow/storagetest"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// pgTableSeq gives each conformance subtest its own table so they stay isolated
// when they share a single Postgres database (env DSN or a testcontainer).
var pgTableSeq atomic.Int64

// TestPostgresConformance runs the shared Storage conformance suite against the
// PostgreSQL backend. The database comes from WORKFLOW_TEST_POSTGRES_DSN or, if
// that is unset, from a throwaway Postgres testcontainer when Docker is available
// (see postgresDSN). When neither is available the test is skipped.
func TestPostgresConformance(t *testing.T) {
	dsn := postgresDSN(t) // skips the test if no database is available

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}

	storagetest.Run(t, func(t *testing.T) workflow.Storage {
		table := fmt.Sprintf("wf_states_%d", pgTableSeq.Add(1))
		store, err := storage.NewPostgresStorage(db, storage.WithTable(table))
		if err != nil {
			t.Fatalf("NewPostgresStorage: %v", err)
		}
		// EnsureSchema (rather than a bare CREATE TABLE) so the conformance suite
		// also runs against the M4 due column and its index.
		if err := store.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("EnsureSchema: %v", err)
		}
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
			_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s_tokens", table))
		})
		return store
	})
}
