// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

func TestNewSQLiteStorageNilDB(t *testing.T) {
	if _, err := storage.NewSQLiteStorage(nil); err == nil {
		t.Fatal("NewSQLiteStorage(nil) should error")
	}
}

func TestSQLiteGenerateTokenSchemaEnabled(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if s.GenerateTokenSchema() == "" {
		t.Fatal("GenerateTokenSchema should emit DDL when the token table is enabled")
	}
}

// TestSQLiteLoadCorruptTokenRow covers the token-JSON decode error path in
// loadState/markingFromTokenJSON: a blanked state blob whose authoritative
// token rows contain unparseable JSON.
func TestSQLiteLoadCorruptTokenRow(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// Instance row with a blanked blob (token rows are authoritative)…
	if _, err := db.ExecContext(ctx,
		"INSERT INTO workflow_states (id, state, version, context) VALUES ('c', '', 1, '{}')"); err != nil {
		t.Fatal(err)
	}
	// …and a corrupt token row.
	if _, err := db.ExecContext(ctx,
		"INSERT INTO workflow_states_tokens (workflow_id, place, token_id, token) VALUES ('c', 'p', 't1', ?)",
		`{not json`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.LoadState(ctx, "c"); err == nil {
		t.Fatal("loading an instance with a corrupt token row should error")
	}
}

func TestSQLiteEnsureSchemaIdempotentAndDueDisabled(t *testing.T) {
	ctx := context.Background()

	// Calling EnsureSchema twice must be a no-op the second time (exercises the
	// tolerated "duplicate column" ALTER path).
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("first EnsureSchema: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatalf("second EnsureSchema (idempotent): %v", err)
	}

	// With the due column disabled, EnsureSchema returns before touching the
	// due index and the backend no longer advertises DueStorage.
	db2 := setupTestDB(t)
	s2, err := storage.NewSQLiteStorage(db2, storage.WithDueColumn(""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.EnsureSchema(ctx); err != nil {
		t.Fatalf("EnsureSchema (due disabled): %v", err)
	}
}

// TestSQLiteTokenDisabledPaths exercises the blob-mode (WithTokenTable(""))
// branches of DeleteState and SaveStateWithDue plus the WithContextColumn
// option, which the default token-enabled tests never reach.
func TestSQLiteTokenDisabledPaths(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db,
		storage.WithTokenTable(""),            // blob mode: no child token table
		storage.WithContextColumn("ctx_json"), // custom context column name
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// GenerateTokenSchema is empty when the token table is disabled.
	if s.GenerateTokenSchema() != "" {
		t.Fatal("GenerateTokenSchema should be empty in blob mode")
	}

	// Plain SaveState also takes the non-tx blob branch.
	if _, err := s.SaveState(ctx, "wf0", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatalf("SaveState (blob mode): %v", err)
	}

	due := time.Now().Add(time.Hour)
	if _, err := s.SaveStateWithDue(ctx, "wf", workflow.NewMarking([]workflow.Place{"a"}), map[string]any{"k": "v"}, 0, &due); err != nil {
		t.Fatalf("SaveStateWithDue (blob mode): %v", err)
	}
	m, cx, _, err := s.LoadState(ctx, "wf")
	if err != nil || !m.HasPlace("a") || cx["k"] != "v" {
		t.Fatalf("LoadState = %v, %v, %v", m, cx, err)
	}

	// DeleteState takes the non-tx branch in blob mode.
	if err := s.DeleteState(ctx, "wf"); err != nil {
		t.Fatalf("DeleteState (blob mode): %v", err)
	}
	if _, _, _, err := s.LoadState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("after delete = %v, want ErrWorkflowNotFound", err)
	}

	// Deleting a missing id is a no-op (not an error).
	if err := s.DeleteState(ctx, "never-existed"); err != nil {
		t.Fatalf("DeleteState(missing) = %v, want nil", err)
	}
}

// TestSQLiteBackfillCorruptBlob covers the decode-error branch of the token
// backfill: a legacy row whose state column holds unparseable JSON.
func TestSQLiteBackfillCorruptBlob(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		"INSERT INTO workflow_states (id, state, version, context) VALUES (?, ?, 1, '{}')",
		"corrupt", `{not valid json`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BackfillTokenStates(ctx); err == nil {
		t.Fatal("BackfillTokenStates should error on an unparseable legacy blob")
	}
}

func TestSQLiteListPlaceTokensPagination(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// Seed five tokens in "pool" across one instance.
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pool"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"pool"}, []workflow.Place{"pool"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("w", def, "pool")
	if err != nil {
		t.Fatal(err)
	}
	wf.ClearPlace("pool")
	for i := range 5 {
		if _, err := wf.CreateToken("pool", workflow.TokenData{"n": i}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.SaveState(ctx, "w", wf.Marking(), nil, 0); err != nil {
		t.Fatal(err)
	}

	// Limit caps.
	if got, err := s.ListPlaceTokens(ctx, "pool", workflow.ListOptions{Limit: 2}); err != nil || len(got) != 2 {
		t.Fatalf("Limit=2 => %d (err %v), want 2", len(got), err)
	}
	// Offset-only uses the SQLite "LIMIT -1 OFFSET" form.
	if got, err := s.ListPlaceTokens(ctx, "pool", workflow.ListOptions{Offset: 3}); err != nil || len(got) != 2 {
		t.Fatalf("Offset=3 => %d (err %v), want 2", len(got), err)
	}
	// Limit+offset.
	if got, err := s.ListPlaceTokens(ctx, "pool", workflow.ListOptions{Limit: 2, Offset: 1}); err != nil || len(got) != 2 {
		t.Fatalf("Limit=2,Offset=1 => %d (err %v), want 2", len(got), err)
	}
}
