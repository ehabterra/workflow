// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

func newVersionedStore(t *testing.T) *storage.SQLiteStorage {
	t.Helper()
	db := newDB(t)
	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatalf("NewSQLiteStorage: %v", err)
	}
	if err := store.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return store
}

func TestVersionedStorage_CreateAndIncrement(t *testing.T) {
	ctx := context.Background()
	store := newVersionedStore(t)

	// Create at version 0 -> 1.
	v, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"a"}), nil, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v != 1 {
		t.Fatalf("create version = %d, want 1", v)
	}

	// Load returns version 1.
	_, _, loaded, err := store.LoadState(ctx, "wf")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded != 1 {
		t.Fatalf("loaded version = %d, want 1", loaded)
	}

	// Update at expected 1 -> 2.
	v, err = store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"b"}), nil, 1)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v != 2 {
		t.Fatalf("update version = %d, want 2", v)
	}
}

func TestVersionedStorage_CreateConflictWhenExists(t *testing.T) {
	ctx := context.Background()
	store := newVersionedStore(t)

	if _, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Second create (expected 0) on an existing id must conflict.
	if _, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"x"}), nil, 0); !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("second create err = %v, want ErrConflict", err)
	}
}

func TestVersionedStorage_StaleUpdateConflicts(t *testing.T) {
	ctx := context.Background()
	store := newVersionedStore(t)

	if _, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Two readers both hold version 1.
	// Writer 1 succeeds, bumping to version 2.
	if _, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"b"}), nil, 1); err != nil {
		t.Fatalf("writer1: %v", err)
	}
	// Writer 2 still thinks version is 1 -> stale -> conflict.
	if _, err := store.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"c"}), nil, 1); !errors.Is(err, workflow.ErrConflict) {
		t.Fatalf("writer2 err = %v, want ErrConflict", err)
	}
	// State reflects only writer 1's change.
	m, _, v, err := store.LoadState(ctx, "wf")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	places := m.Places()
	if v != 2 || len(places) != 1 || places[0] != "b" {
		t.Fatalf("after conflict: version=%d places=%v, want version 2 places [b]", v, places)
	}
}
