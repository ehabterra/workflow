// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	_ "github.com/mattn/go-sqlite3"
)

// TestSQLiteOperationsOnClosedDB drives every top-level operation against a
// closed *sql.DB so the backend's "database error" branches (query/exec
// failures, transaction-begin failures) are exercised without a fault-injecting
// driver. Every call must surface an error rather than panicking.
func TestSQLiteOperationsOnClosedDB(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// Seed one row so read paths have something to attempt before the close.
	if _, err := s.SaveState(ctx, "seed", workflow.NewMarking([]workflow.Place{"a"}), nil, 0); err != nil {
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
		{"EnsureSchema", s.EnsureSchema(ctx)},
		{"SaveState", second(s.SaveState(ctx, "x", workflow.NewMarking([]workflow.Place{"a"}), nil, 0))},
		{"SaveStateWithDue", second(s.SaveStateWithDue(ctx, "x", workflow.NewMarking([]workflow.Place{"a"}), nil, 0, &due))},
		{"LoadState", fourth(s.LoadState(ctx, "seed"))},
		{"DeleteState", s.DeleteState(ctx, "seed")},
		{"ListIDs", secondSlice(s.ListIDs(ctx, workflow.ListOptions{}))},
		{"ListDue", secondSlice(s.ListDue(ctx, due, 0))},
		{"ListPlaceTokens", secondTokens(s.ListPlaceTokens(ctx, "a", workflow.ListOptions{}))},
	}
	for _, c := range checks {
		if c.err == nil {
			t.Errorf("%s on a closed DB returned nil error, want a failure", c.name)
		}
	}
}

func second(_ int64, err error) error                                     { return err }
func fourth(_ workflow.Marking, _ map[string]any, _ int64, e error) error { return e }
func secondSlice(_ []string, err error) error                             { return err }
func secondTokens(_ []workflow.PlacedToken, err error) error              { return err }
