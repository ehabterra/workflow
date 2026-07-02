package workflow_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

// TestSentinelErrors verifies that engine errors wrap the exported sentinels so
// callers can branch on them with errors.Is rather than matching strings.
func TestSentinelErrors(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"start"}, []workflow.Place{"end"})},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	t.Run("invalid workflow name", func(t *testing.T) {
		_, err := workflow.NewWorkflow("", def, "start")
		if !errors.Is(err, workflow.ErrInvalidWorkflow) {
			t.Fatalf("got %v, want wrap of ErrInvalidWorkflow", err)
		}
	})

	t.Run("nil definition", func(t *testing.T) {
		_, err := workflow.NewWorkflow("wf", nil, "start")
		if !errors.Is(err, workflow.ErrInvalidDefinition) {
			t.Fatalf("got %v, want wrap of ErrInvalidDefinition", err)
		}
	})

	t.Run("invalid initial place", func(t *testing.T) {
		_, err := workflow.NewWorkflow("wf", def, "nope")
		if !errors.Is(err, workflow.ErrInvalidPlace) {
			t.Fatalf("got %v, want wrap of ErrInvalidPlace", err)
		}
	})

	t.Run("registry not found", func(t *testing.T) {
		r := workflow.NewRegistry()
		_, err := r.Workflow("missing")
		if !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("got %v, want wrap of ErrWorkflowNotFound", err)
		}
	})

	t.Run("registry already exists", func(t *testing.T) {
		r := workflow.NewRegistry()
		wf, err := workflow.NewWorkflow("dup", def, "start")
		if err != nil {
			t.Fatalf("NewWorkflow: %v", err)
		}
		if err := r.AddWorkflow(wf); err != nil {
			t.Fatalf("AddWorkflow: %v", err)
		}
		if err := r.AddWorkflow(wf); !errors.Is(err, workflow.ErrWorkflowExists) {
			t.Fatalf("got %v, want wrap of ErrWorkflowExists", err)
		}
	})

	t.Run("storage state not found", func(t *testing.T) {
		db, err := sql.Open("sqlite3", ":memory:")
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		defer func() { _ = db.Close() }()

		store, err := storage.NewSQLiteStorage(db)
		if err != nil {
			t.Fatalf("NewSQLiteStorage: %v", err)
		}
		if err := storage.Initialize(db, store.GenerateSchema()); err != nil {
			t.Fatalf("Initialize: %v", err)
		}

		_, _, err = store.LoadState(context.Background(), "does-not-exist")
		if !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("got %v, want wrap of ErrWorkflowNotFound", err)
		}
	})
}
