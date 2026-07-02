package storage_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// postgresDSN returns a Postgres connection string for the conformance test,
// implementing the "both" strategy: use WORKFLOW_TEST_POSTGRES_DSN when set (CI
// supplies a service container this way), otherwise start a throwaway Postgres
// container via testcontainers when Docker is available. If neither is available
// the test is skipped.
func postgresDSN(t *testing.T) string {
	t.Helper()

	if dsn := os.Getenv("WORKFLOW_TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}

	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("workflow"),
		tcpostgres.WithUsername("workflow"),
		tcpostgres.WithPassword("workflow"),
		testcontainers.WithWaitStrategy(
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("no Postgres available: set WORKFLOW_TEST_POSTGRES_DSN or start Docker (%v)", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return dsn
}
