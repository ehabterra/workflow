// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// newPostgresStore spins up (or connects to) Postgres and returns a store on a
// uniquely-named table that is dropped on cleanup.
func newPostgresStore(t *testing.T, opts ...storage.Option) (*storage.PostgresStorage, *sql.DB, string) {
	t.Helper()
	dsn := postgresDSN(t) // skips if no Postgres/Docker
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	table := fmt.Sprintf("wfcov_%d", pgTableSeq.Add(1))
	store, err := storage.NewPostgresStorage(db, append([]storage.Option{storage.WithTable(table)}, opts...)...)
	if err != nil {
		t.Fatalf("NewPostgresStorage: %v", err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s", table))
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP TABLE IF EXISTS %s_tokens", table))
		_ = db.Close()
	})
	return store, db, table
}

func TestPostgresTokenDisabledPaths(t *testing.T) {
	ctx := context.Background()
	store, _, _ := newPostgresStore(t, storage.WithTokenTable(""))

	if store.GenerateTokenSchema() != "" {
		t.Fatal("GenerateTokenSchema should be empty in blob mode")
	}

	// SaveState / SaveStateWithDue / DeleteState take their non-tx blob branches.
	if _, err := store.SaveState(ctx, "b0", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatalf("SaveState (blob): %v", err)
	}
	due := time.Now().Add(time.Hour)
	if _, err := store.SaveStateWithDue(ctx, "b1", workflow.NewMarking([]workflow.Place{"a"}), nil, 0, &due); err != nil {
		t.Fatalf("SaveStateWithDue (blob): %v", err)
	}
	if err := store.DeleteState(ctx, "b0"); err != nil {
		t.Fatalf("DeleteState (blob): %v", err)
	}
	if _, _, _, err := store.LoadState(ctx, "b0"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("after delete = %v, want ErrWorkflowNotFound", err)
	}
}

func TestNewPostgresStorageNilDB(t *testing.T) {
	if _, err := storage.NewPostgresStorage(nil); err == nil {
		t.Fatal("NewPostgresStorage(nil) should error")
	}
}

func TestPostgresTxMethodVariants(t *testing.T) {
	ctx := context.Background()
	store, db, _ := newPostgresStore(t)

	// SaveStateTx visible inside the tx, gone after rollback.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveStateTx(ctx, tx, "wf", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatalf("SaveStateTx: %v", err)
	}
	if m, _, _, err := store.LoadStateTx(ctx, tx, "wf"); err != nil || !m.HasPlace("a") {
		t.Fatalf("LoadStateTx = %v, %v; want [a]", m, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.LoadState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("after rollback = %v, want ErrWorkflowNotFound", err)
	}

	// Commit persists; DeleteStateTx removes in a tx.
	if err := storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		_, err := store.SaveStateTx(ctx, tx, "wf", workflow.NewMarking([]workflow.Place{"b"}), nil, 0)
		return err
	}); err != nil {
		t.Fatalf("RunInTx save: %v", err)
	}
	if m, _, _, err := store.LoadState(ctx, "wf"); err != nil || !m.HasPlace("b") {
		t.Fatalf("after commit = %v, %v; want [b]", m, err)
	}
	if err := storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
		return store.DeleteStateTx(ctx, tx, "wf")
	}); err != nil {
		t.Fatalf("RunInTx delete: %v", err)
	}
	if _, _, _, err := store.LoadState(ctx, "wf"); !errors.Is(err, workflow.ErrWorkflowNotFound) {
		t.Fatalf("after DeleteStateTx = %v, want ErrWorkflowNotFound", err)
	}
}

// TestPostgresOperationsOnClosedDB mirrors the SQLite closed-DB fault test for
// the Postgres backend, exercising its database-error branches.
func TestPostgresOperationsOnClosedDB(t *testing.T) {
	ctx := context.Background()
	store, db, _ := newPostgresStore(t)
	if _, err := store.SaveState(ctx, "seed", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	due := time.Now().Add(time.Hour)
	checks := []struct {
		name string
		err  error
	}{
		{"EnsureSchema", store.EnsureSchema(ctx)},
		{"SaveState", second(store.SaveState(ctx, "x", workflow.NewMarking([]workflow.Place{"a"}), nil, 0))},
		{"SaveStateWithDue", second(store.SaveStateWithDue(ctx, "x", workflow.NewMarking([]workflow.Place{"a"}), nil, 0, &due))},
		{"LoadState", fourth(store.LoadState(ctx, "seed"))},
		{"DeleteState", store.DeleteState(ctx, "seed")},
		{"ListIDs", secondSlice(store.ListIDs(ctx, workflow.ListOptions{}))},
		{"ListDue", secondSlice(store.ListDue(ctx, due, 0))},
		{"ListPlaceTokens", secondTokens(store.ListPlaceTokens(ctx, "a", workflow.ListOptions{}))},
		{"BackfillTokenStates", third(store.BackfillTokenStates(ctx))},
	}
	for _, c := range checks {
		if c.err == nil {
			t.Errorf("%s on a closed Postgres DB returned nil error, want a failure", c.name)
		}
	}
}

func third(_ int, err error) error { return err }

func TestPostgresBackfillAndTokenSchema(t *testing.T) {
	ctx := context.Background()
	store, db, table := newPostgresStore(t)

	if store.GenerateTokenSchema() == "" {
		t.Fatal("GenerateTokenSchema should emit DDL when the token table is enabled")
	}

	// Seed a legacy blob row (marking JSON in the state column, no token rows).
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("INSERT INTO %s (id, state, version, context) VALUES ($1, $2, 1, '{}')", table),
		"lg1", `{"payable":[{"id":"t1","data":{"amount":5}}]}`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.SaveState(ctx, "new1", workflow.NewMarking([]workflow.Place{"draft"}), nil, 0); err != nil {
		t.Fatal(err)
	}

	migrated, err := store.BackfillTokenStates(ctx)
	if err != nil {
		t.Fatalf("BackfillTokenStates: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1", migrated)
	}
	// The legacy row's marking survives and the version does NOT bump.
	if m, _, v, err := store.LoadState(ctx, "lg1"); err != nil || v != 1 || !m.HasToken("payable", "t1") {
		t.Fatalf("lg1 after backfill: %v v%d %+v", err, v, m)
	}
	// Idempotent.
	if migrated, err = store.BackfillTokenStates(ctx); err != nil || migrated != 0 {
		t.Fatalf("second backfill: %v migrated %d, want 0", err, migrated)
	}
}
