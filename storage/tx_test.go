// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

// newDB opens a fresh in-memory SQLite database for a test.
func newDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestRunInTx_AtomicStateAndHistory verifies that a workflow state save and a
// history append committed through the same transaction are all-or-nothing: on
// success both are visible, and on a mid-transaction failure neither is.
func TestRunInTx_AtomicStateAndHistory(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("init state schema: %v", err)
	}

	hist := history.NewSQLiteHistory(db)
	if err := hist.Initialize(ctx); err != nil {
		t.Fatalf("init history schema: %v", err)
	}

	rec := &history.TransitionRecord{
		WorkflowID: "wf1", FromState: "draft", ToState: "review", Transition: "submit",
	}

	// --- Success path: both writes commit together. ---
	err = storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := store.SaveStateTx(ctx, tx, "wf1", workflow.NewMarking([]workflow.Place{"review"}), nil, 0); err != nil {
			return err
		}
		return hist.SaveTransitionTx(ctx, tx, rec)
	})
	if err != nil {
		t.Fatalf("RunInTx (success): %v", err)
	}

	m, _, _, err := store.LoadState(ctx, "wf1")
	if err != nil {
		t.Fatalf("LoadState after commit: %v", err)
	}
	places := m.Places()
	if len(places) != 1 || places[0] != "review" {
		t.Fatalf("state after commit = %v, want [review]", places)
	}
	recs, err := hist.ListHistory(ctx, "wf1", history.QueryOptions{})
	if err != nil {
		t.Fatalf("ListHistory after commit: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("history after commit = %d records, want 1", len(recs))
	}

	// --- Failure path: the second write fails, so the state change must roll back. ---
	wantErr := errors.New("boom: simulated crash mid-transition")
	err = storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		// This state change moves wf1 to "done"...
		if _, err := store.SaveStateTx(ctx, tx, "wf1", workflow.NewMarking([]workflow.Place{"done"}), nil, 1); err != nil {
			return err
		}
		// ...but the transition record fails to persist, aborting the whole tx.
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunInTx (failure) err = %v, want wrap of %v", err, wantErr)
	}

	// State must be unchanged: still "review", not "done".
	m, _, _, err = store.LoadState(ctx, "wf1")
	if err != nil {
		t.Fatalf("LoadState after rollback: %v", err)
	}
	places = m.Places()
	if len(places) != 1 || places[0] != "review" {
		t.Fatalf("state after rollback = %v, want it unchanged at [review]", places)
	}
	// History must not have gained a second record.
	recs, err = hist.ListHistory(ctx, "wf1", history.QueryOptions{})
	if err != nil {
		t.Fatalf("ListHistory after rollback: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("history after rollback = %d records, want it unchanged at 1", len(recs))
	}
}

// TestRunInTx_NilDB verifies RunInTx rejects a nil database handle.
func TestRunInTx_NilDB(t *testing.T) {
	called := false
	err := storage.RunInTx(context.Background(), nil, func(*sql.Tx) error {
		called = true
		return nil
	})
	if err == nil {
		t.Fatal("expected error for nil db")
	}
	if called {
		t.Fatal("fn should not run with a nil db")
	}
}

// TestRunInTx_RollsBackOnPanic verifies a panic in fn rolls back and re-raises.
func TestRunInTx_RollsBackOnPanic(t *testing.T) {
	ctx := context.Background()
	db := newDB(t)

	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("init: %v", err)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic to propagate out of RunInTx")
		}
		// The write inside the panicking tx must not have committed.
		if _, _, _, err := store.LoadState(ctx, "wfP"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
			t.Fatalf("state after panic rollback err = %v, want ErrWorkflowNotFound", err)
		}
	}()

	_ = storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		if _, err := store.SaveStateTx(ctx, tx, "wfP", workflow.NewMarking([]workflow.Place{"x"}), nil, 0); err != nil {
			return err
		}
		panic("simulated crash")
	})
}
