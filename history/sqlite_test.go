// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package history_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow/history"
	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	return db
}

func TestSQLiteHistory_Basic(t *testing.T) {
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	rec := &history.TransitionRecord{
		WorkflowID: "wf1",
		FromState:  "draft",
		ToState:    "review",
		Transition: "submit_for_review",
		Notes:      "test note",
		Actor:      "user1",
		CreatedAt:  time.Now(),
	}
	if err := h.SaveTransition(context.Background(), rec); err != nil {
		t.Fatalf("failed to save transition: %v", err)
	}

	hist, err := h.ListHistory(context.Background(), "wf1", history.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 record, got %d", len(hist))
	}
	if hist[0].FromState != "draft" || hist[0].ToState != "review" {
		t.Errorf("unexpected states: %+v", hist[0])
	}
}

func TestSQLiteHistory_CustomFields(t *testing.T) {
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db, history.WithCustomFields(map[string]string{
		"ip_address": "ip_address TEXT",
		"user_agent": "user_agent TEXT",
	}))
	if err := h.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	rec := &history.TransitionRecord{
		WorkflowID: "wf2",
		FromState:  "review",
		ToState:    "approved",
		Transition: "approve",
		Notes:      "approved by admin",
		Actor:      "admin",
		CreatedAt:  time.Now(),
		CustomFields: map[string]any{
			"ip_address": "127.0.0.1",
			"user_agent": "test-agent",
		},
	}
	if err := h.SaveTransition(context.Background(), rec); err != nil {
		t.Fatalf("failed to save transition with custom fields: %v", err)
	}

	hist, err := h.ListHistory(context.Background(), "wf2", history.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist) != 1 {
		t.Fatalf("expected 1 record, got %d", len(hist))
	}
	cf := hist[0].CustomFields
	if cf["ip_address"] != "127.0.0.1" || cf["user_agent"] != "test-agent" {
		t.Errorf("unexpected custom fields: %+v", cf)
	}
}

func TestSQLiteHistory_PaginationAndFiltering(t *testing.T) {
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}
	now := time.Now()
	for i := 0; i < 10; i++ {
		rec := &history.TransitionRecord{
			WorkflowID: "wf3",
			FromState:  "s1",
			ToState:    "s2",
			Transition: "t",
			Notes:      "n",
			Actor:      "actor",
			CreatedAt:  now.Add(time.Duration(i) * time.Minute),
		}
		if err := h.SaveTransition(context.Background(), rec); err != nil {
			t.Fatalf("failed to save transition: %v", err)
		}
	}
	// Test limit
	hist, err := h.ListHistory(context.Background(), "wf3", history.QueryOptions{Limit: 3})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist) != 3 {
		t.Errorf("expected 3 records, got %d", len(hist))
	}
	// Test offset
	hist2, err := h.ListHistory(context.Background(), "wf3", history.QueryOptions{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist2) != 2 {
		t.Errorf("expected 2 records, got %d", len(hist2))
	}
	// Test filtering by actor
	hist3, err := h.ListHistory(context.Background(), "wf3", history.QueryOptions{Actor: "actor"})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist3) != 10 {
		t.Errorf("expected 10 records, got %d", len(hist3))
	}
}

func TestSQLiteHistory_WithTable(t *testing.T) {
	db := setupTestDB(t)

	// Test with custom table name
	customTable := "custom_history_table"
	h := history.NewSQLiteHistory(db, history.WithTable(customTable))
	if err := h.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// Verify table name is used
	schema := h.GenerateSchema()
	if !contains(schema, customTable) {
		t.Errorf("Schema does not contain custom table name %q", customTable)
	}

	// Test saving and loading with custom table
	rec := &history.TransitionRecord{
		WorkflowID: "wf_custom",
		FromState:  "draft",
		ToState:    "review",
		Transition: "submit",
		Notes:      "test",
		Actor:      "user",
		CreatedAt:  time.Now(),
	}
	if err := h.SaveTransition(context.Background(), rec); err != nil {
		t.Fatalf("failed to save transition: %v", err)
	}

	hist, err := h.ListHistory(context.Background(), "wf_custom", history.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist) != 1 {
		t.Errorf("expected 1 record, got %d", len(hist))
	}

	// Test with default table name
	h2 := history.NewSQLiteHistory(db)
	if err := h2.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	schema2 := h2.GenerateSchema()
	if !contains(schema2, "transition_history") {
		t.Error("Default table name 'transition_history' not found in schema")
	}
}

func TestSQLiteHistory_EdgeCases(t *testing.T) {
	db := setupTestDB(t)
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(context.Background()); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	// Test saving transition with empty workflow ID
	rec := &history.TransitionRecord{
		WorkflowID: "",
		FromState:  "draft",
		ToState:    "review",
		Transition: "submit",
		Notes:      "",
		Actor:      "",
		CreatedAt:  time.Now(),
	}
	if err := h.SaveTransition(context.Background(), rec); err != nil {
		t.Fatalf("failed to save transition with empty fields: %v", err)
	}

	// Test listing history for non-existent workflow
	hist, err := h.ListHistory(context.Background(), "non_existent", history.QueryOptions{})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist) != 0 {
		t.Errorf("expected 0 records, got %d", len(hist))
	}

	// Test with negative limit
	hist2, err := h.ListHistory(context.Background(), "wf1", history.QueryOptions{Limit: -1})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	// Should handle negative limit gracefully
	_ = hist2

	// Test with very large offset
	hist3, err := h.ListHistory(context.Background(), "wf1", history.QueryOptions{Offset: 1000000})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	if len(hist3) != 0 {
		t.Errorf("expected 0 records with large offset, got %d", len(hist3))
	}

	// Test with empty actor filter
	hist4, err := h.ListHistory(context.Background(), "wf1", history.QueryOptions{Actor: ""})
	if err != nil {
		t.Fatalf("failed to list history: %v", err)
	}
	_ = hist4
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			strings.Contains(s, substr))))
}
