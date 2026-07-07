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
		if err := store.EnsureSchema(context.Background()); err != nil {
			t.Fatalf("Initialize: %v", err)
		}

		_, _, _, err = store.LoadState(context.Background(), "does-not-exist")
		if !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("got %v, want wrap of ErrWorkflowNotFound", err)
		}
	})
}

// Both concrete block reasons must still satisfy the general sentinel, so
// callers testing errors.Is(err, ErrTransitionNotAllowed) keep working.
func TestBlockedErrors_SatisfyGeneralSentinel(t *testing.T) {
	for _, err := range []error{workflow.ErrNotEnabled, workflow.ErrGuardRejected} {
		if !errors.Is(err, workflow.ErrTransitionNotAllowed) {
			t.Errorf("errors.Is(%v, ErrTransitionNotAllowed) = false, want true", err)
		}
		if !errors.Is(err, err) {
			t.Errorf("errors.Is(%v, itself) = false, want true", err)
		}
	}
	if errors.Is(workflow.ErrNotEnabled, workflow.ErrGuardRejected) {
		t.Error("ErrNotEnabled should not match ErrGuardRejected")
	}
}

// A transition whose input place is unmarked reports ErrNotEnabled; a transition
// enabled by the marking but refused by a guard reports ErrGuardRejected.
func TestApply_DistinguishesNotEnabledFromGuardRejected(t *testing.T) {
	guard, err := workflow.NewExpressionConstraint("workflow.Context('ok') == true")
	if err != nil {
		t.Fatalf("NewExpressionConstraint: %v", err)
	}
	tr := workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})
	tr.AddConstraint(guard)
	def, err := workflow.NewDefinition([]workflow.Place{"a", "b"}, []workflow.Transition{*tr})
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	// Enabled place, guard rejects -> ErrGuardRejected.
	wf, _ := workflow.NewWorkflow("wf", def, "a")
	wf.SetContext("ok", false)
	err = wf.ApplyTransition("go")
	if !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("guard-blocked ApplyTransition err = %v, want ErrGuardRejected", err)
	}
	if !errors.Is(err, workflow.ErrTransitionNotAllowed) {
		t.Fatalf("ErrGuardRejected must still satisfy ErrTransitionNotAllowed, got %v", err)
	}

	// Starting past 'a' leaves the transition unenabled -> ErrNotEnabled.
	wf2, _ := workflow.NewWorkflow("wf2", def, "b")
	err = wf2.ApplyTransition("go")
	if !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("not-enabled ApplyTransition err = %v, want ErrNotEnabled", err)
	}
	if errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("not-enabled error should not be ErrGuardRejected, got %v", err)
	}
}
