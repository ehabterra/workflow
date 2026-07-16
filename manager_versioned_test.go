// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

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

// TestManager_VersionedSave_Conflict verifies the roadmap M1.2 acceptance: two
// writers that both loaded the same workflow version cannot both save — the
// second save fails with ErrConflict.
func TestManager_VersionedSave_Conflict(t *testing.T) {
	ctx := context.Background()

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

	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "a", "b"},
		[]workflow.Transition{
			*workflow.MustNewTransition("toA", []workflow.Place{"start"}, []workflow.Place{"a"}),
			*workflow.MustNewTransition("toB", []workflow.Place{"start"}, []workflow.Place{"b"}),
		},
	)
	if err != nil {
		t.Fatalf("NewDefinition: %v", err)
	}

	manager := workflow.NewManager(workflow.NewRegistry(), store)

	// Create the workflow (persisted at version 1).
	if _, err := manager.CreateWorkflow(ctx, "wf", def, "start"); err != nil {
		t.Fatalf("CreateWorkflow: %v", err)
	}

	// Two independent managers load the same workflow at the same version,
	// simulating two processes/goroutines racing on the same instance.
	mgr1 := workflow.NewManager(workflow.NewRegistry(), store)
	mgr2 := workflow.NewManager(workflow.NewRegistry(), store)

	wf1, err := mgr1.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("mgr1 load: %v", err)
	}
	wf2, err := mgr2.LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("mgr2 load: %v", err)
	}
	if wf1.Version() != wf2.Version() {
		t.Fatalf("expected both loads at same version, got %d and %d", wf1.Version(), wf2.Version())
	}

	// Writer 1 applies a transition and saves successfully.
	if err := wf1.ApplyTransition("toA"); err != nil {
		t.Fatalf("wf1 apply: %v", err)
	}
	if err := mgr1.SaveWorkflow(ctx, "wf", wf1); err != nil {
		t.Fatalf("mgr1 save: %v", err)
	}

	// Writer 2 applies a conflicting transition on its stale copy and must fail.
	if err := wf2.ApplyTransition("toB"); err != nil {
		t.Fatalf("wf2 apply: %v", err)
	}
	if err := mgr2.SaveWorkflow(ctx, "wf", wf2); !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("mgr2 save err = %v, want ErrConflict", err)
	}

	// The persisted state reflects only writer 1's change.
	fresh, err := workflow.NewManager(workflow.NewRegistry(), store).LoadWorkflow(ctx, "wf", def)
	if err != nil {
		t.Fatalf("fresh load: %v", err)
	}
	if got := fresh.CurrentPlaces(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("persisted state = %v, want [a] (writer 1 won)", got)
	}
}
