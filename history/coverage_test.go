// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package history_test

import (
	"context"
	"testing"
	"time"

	"github.com/ehabterra/workflow/history"
	_ "github.com/mattn/go-sqlite3"
)

func TestSaveTransitionTxCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	rec := &history.TransitionRecord{
		WorkflowID: "wf-tx",
		FromState:  "a",
		ToState:    "b",
		Transition: "go",
		CreatedAt:  time.Now(),
	}

	// Committed transaction persists the record.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.SaveTransitionTx(ctx, tx, rec); err != nil {
		t.Fatalf("SaveTransitionTx = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := h.ListHistory(ctx, "wf-tx", history.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("after commit: %d records, want 1", len(got))
	}

	// Rolled-back transaction leaves no trace.
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := *rec
	rec2.ToState = "c"
	if err := h.SaveTransitionTx(ctx, tx2, &rec2); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err = h.ListHistory(ctx, "wf-tx", history.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("after rollback: %d records, want still 1", len(got))
	}
}

func TestListHistoryQueryError(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	// Create the table WITHOUT any custom columns.
	if err := history.NewSQLiteHistory(db).Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	// A second store expects a column the table does not have, so its SELECT
	// (which lists the custom column) fails at query time.
	h := history.NewSQLiteHistory(db, history.WithCustomFields(map[string]string{"ghost": "TEXT"}))
	if _, err := h.ListHistory(ctx, "wf", history.QueryOptions{}); err == nil {
		t.Fatal("ListHistory selecting a missing column should return an error")
	}
}

func TestNewPostgresHistoryConstructs(t *testing.T) {
	// Construction only — no connection is made. Exercises the Postgres-dialect
	// constructor path; the generated SQL is validated by the storagetest
	// conformance suite against a live Postgres.
	db := setupTestDB(t)
	h := history.NewPostgresHistory(db, history.WithTable("audit"))
	if h == nil {
		t.Fatal("NewPostgresHistory returned nil")
	}
}

func TestListHistoryPagination(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := range 5 {
		if err := h.SaveTransition(ctx, &history.TransitionRecord{
			WorkflowID: "wf-page",
			FromState:  "s",
			ToState:    "t",
			Transition: "go",
			CreatedAt:  base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Limit caps the result set.
	page, err := h.ListHistory(ctx, "wf-page", history.QueryOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 2 {
		t.Fatalf("Limit=2 returned %d, want 2", len(page))
	}

	// Offset skips.
	rest, err := h.ListHistory(ctx, "wf-page", history.QueryOptions{Limit: 10, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 3 {
		t.Fatalf("Offset=2 returned %d, want 3", len(rest))
	}

	// Offset with no limit uses the dialect's no-limit clause.
	offsetOnly, err := h.ListHistory(ctx, "wf-page", history.QueryOptions{Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(offsetOnly) != 4 {
		t.Fatalf("Offset-only returned %d, want 4", len(offsetOnly))
	}
}

func TestListHistoryFilters(t *testing.T) {
	ctx := context.Background()
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	recs := []history.TransitionRecord{
		{WorkflowID: "wf-f", FromState: "a", ToState: "b", Transition: "submit", Actor: "alice", CreatedAt: base},
		{WorkflowID: "wf-f", FromState: "b", ToState: "c", Transition: "approve", Actor: "bob", CreatedAt: base.Add(time.Hour)},
		{WorkflowID: "wf-f", FromState: "c", ToState: "d", Transition: "approve", Actor: "alice", CreatedAt: base.Add(2 * time.Hour)},
	}
	for i := range recs {
		if err := h.SaveTransition(ctx, &recs[i]); err != nil {
			t.Fatal(err)
		}
	}

	// Filter by actor.
	if got, err := h.ListHistory(ctx, "wf-f", history.QueryOptions{Actor: "alice"}); err != nil || len(got) != 2 {
		t.Fatalf("Actor filter = %d records (err %v), want 2", len(got), err)
	}
	// Filter by transition.
	if got, err := h.ListHistory(ctx, "wf-f", history.QueryOptions{Transition: "approve"}); err != nil || len(got) != 2 {
		t.Fatalf("Transition filter = %d records (err %v), want 2", len(got), err)
	}
	// Filter by date window [base+30m, base+90m] => only the middle record.
	from := base.Add(30 * time.Minute)
	to := base.Add(90 * time.Minute)
	if got, err := h.ListHistory(ctx, "wf-f", history.QueryOptions{FromDate: &from, ToDate: &to}); err != nil || len(got) != 1 {
		t.Fatalf("date filter = %d records (err %v), want 1", len(got), err)
	}
}
