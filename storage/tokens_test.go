package storage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
)

// TestSQLiteTokenRows pins the normalized wire format: saves mirror the
// marking into one row per token and blank the state blob; updates replace
// the rows; deletes remove them.
func TestSQLiteTokenRows(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	m := workflow.NewMarking(nil)
	m.AddToken("payable", workflow.NewTokenWithID("t1", workflow.TokenData{"amount": float64(9)}))
	m.AddPlace("review")
	if _, err := s.SaveState(ctx, "wf", m, nil, 0); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	var blob string
	if err := db.QueryRowContext(ctx, "SELECT state FROM workflow_states WHERE id = 'wf'").Scan(&blob); err != nil {
		t.Fatalf("read state column: %v", err)
	}
	if blob != "" {
		t.Fatalf("state blob = %q, want blank (marking lives in token rows)", blob)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_states_tokens WHERE workflow_id = 'wf'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("token rows = %d, want 2 (one colored, one presence)", n)
	}

	// The round-trip preserves identity, data, and presence places.
	loaded, _, _, err := s.LoadState(ctx, "wf")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if !loaded.HasToken("payable", "t1") || !loaded.HasPlace("review") {
		t.Fatalf("marking lost in normalization: %+v", loaded.AllTokens())
	}

	// An update replaces the rows wholesale (simple flavor).
	if _, err := s.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"done"}), nil, 1); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_states_tokens WHERE workflow_id = 'wf'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("token rows after update = %d, want 1", n)
	}

	// Delete removes the instance AND its rows.
	if err := s.DeleteState(ctx, "wf"); err != nil {
		t.Fatalf("DeleteState: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_states_tokens WHERE workflow_id = 'wf'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("token rows after delete = %d, want 0", n)
	}
}

// TestSQLiteLegacyBlobLoad: a row written by a pre-token-table binary (marking
// JSON in the state column, no token rows) loads as-is — the non-empty blob is
// authoritative — and the instance's next save normalizes it.
func TestSQLiteLegacyBlobLoad(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	legacy := `{"payable":[{"id":"t1","data":{"amount":7}}],"review":[{"id":""}]}`
	if _, err := db.ExecContext(ctx,
		"INSERT INTO workflow_states (id, state, version, context) VALUES ('old', ?, 3, '{}')", legacy); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	m, _, version, err := s.LoadState(ctx, "old")
	if err != nil {
		t.Fatalf("LoadState(legacy): %v", err)
	}
	if version != 3 || !m.HasToken("payable", "t1") || !m.HasPlace("review") {
		t.Fatalf("legacy load wrong: v%d %+v", version, m.AllTokens())
	}

	// The next save normalizes: blob blanked, rows written.
	if _, err := s.SaveState(ctx, "old", m, nil, version); err != nil {
		t.Fatalf("normalizing save: %v", err)
	}
	var blob string
	var n int
	if err := db.QueryRowContext(ctx, "SELECT state, (SELECT COUNT(*) FROM workflow_states_tokens WHERE workflow_id='old') FROM workflow_states WHERE id='old'").Scan(&blob, &n); err != nil {
		t.Fatal(err)
	}
	if blob != "" || n != 2 {
		t.Fatalf("after normalizing save: blob=%q rows=%d, want blank + 2", blob, n)
	}
}

// TestSQLiteBackfillTokenStates migrates legacy rows eagerly (so the
// read-model sees instances that have not saved since the upgrade) and is
// idempotent.
func TestSQLiteBackfillTokenStates(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	// Two legacy rows (one with colored tokens, one compact place-array
	// form) and one already-normalized row.
	seed := func(id, blob string) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			"INSERT INTO workflow_states (id, state, version, context) VALUES (?, ?, 1, '{}')", id, blob); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("lg1", `{"payable":[{"id":"t1","data":{"amount":5}}]}`)
	seed("lg2", `["draft"]`)
	if _, err := s.SaveState(ctx, "new1", workflow.NewMarking([]workflow.Place{"draft"}), nil, 0); err != nil {
		t.Fatal(err)
	}

	migrated, err := s.BackfillTokenStates(ctx)
	if err != nil {
		t.Fatalf("BackfillTokenStates: %v", err)
	}
	if migrated != 2 {
		t.Fatalf("migrated = %d, want 2", migrated)
	}

	// Blobs blanked, rows present, markings intact, read-model complete.
	var blanks int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM workflow_states WHERE state = ''").Scan(&blanks); err != nil {
		t.Fatal(err)
	}
	if blanks != 3 {
		t.Fatalf("blank blobs = %d, want all 3", blanks)
	}
	m, _, v, err := s.LoadState(ctx, "lg1")
	if err != nil || v != 1 || !m.HasToken("payable", "t1") {
		t.Fatalf("lg1 after backfill: %v v%d %+v (version must NOT bump)", err, v, m)
	}
	got, err := s.ListPlaceTokens(ctx, "draft", workflow.ListOptions{})
	if err != nil || len(got) != 2 {
		t.Fatalf("read-model after backfill: %v, %d draft tokens, want 2 (lg2 + new1)", err, len(got))
	}

	// Idempotent: nothing left to migrate.
	if migrated, err = s.BackfillTokenStates(ctx); err != nil || migrated != 0 {
		t.Fatalf("second backfill: %v, migrated %d, want 0", err, migrated)
	}
}

// TestSQLiteTokenTableDisabled: WithTokenTable("") restores the pre-token
// blob format exactly, and the read-model reports unsupported.
func TestSQLiteTokenTableDisabled(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db, storage.WithTokenTable(""))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if s.GenerateTokenSchema() != "" {
		t.Fatal("disabled mode must not emit token DDL")
	}

	if _, err := s.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"draft"}), nil, 0); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	var blob string
	if err := db.QueryRowContext(ctx, "SELECT state FROM workflow_states WHERE id = 'wf'").Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if blob != `["draft"]` {
		t.Fatalf("blob = %q, want the legacy compact form", blob)
	}
	m, _, _, err := s.LoadState(ctx, "wf")
	if err != nil || !m.HasPlace("draft") {
		t.Fatalf("load in disabled mode: %v %+v", err, m)
	}
	if _, err := s.ListPlaceTokens(ctx, "draft", workflow.ListOptions{}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("ListPlaceTokens disabled: %v, want ErrUnsupported", err)
	}
	if _, err := s.BackfillTokenStates(ctx); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("BackfillTokenStates disabled: %v, want ErrUnsupported", err)
	}
}

// TestSQLiteCustomTokenTable: WithTokenTable names the child table explicitly.
func TestSQLiteCustomTokenTable(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db, storage.WithTokenTable("my_tokens"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveState(ctx, "wf", workflow.NewMarking([]workflow.Place{"draft"}), nil, 0); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM my_tokens").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("custom token table rows = %d, want 1", n)
	}
}

// TestManagerListPlaceTokens drives the read-model through the Manager — the
// shared-pool query a host actually writes: "every payable token in the
// system", one call, no instance loads.
func TestManagerListPlaceTokens(t *testing.T) {
	db := setupTestDB(t)
	s, err := storage.NewSQLiteStorage(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	mgr := workflow.NewManager(workflow.NewRegistry(), s)

	pay, err := workflow.NewTransition("pay", []workflow.Place{"payable"}, []workflow.Place{"paid"})
	if err != nil {
		t.Fatal(err)
	}
	def, err := workflow.NewDefinition([]workflow.Place{"payable", "paid"}, []workflow.Transition{*pay})
	if err != nil {
		t.Fatal(err)
	}

	for i, amount := range []float64{10, 20} {
		id := fmt.Sprintf("pool-%d", i)
		wf, err := mgr.CreateWorkflowFromMarking(ctx, id, def, workflow.NewMarking(nil))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := wf.CreateToken("payable", workflow.TokenData{"amount": amount}); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SaveWorkflow(ctx, id, wf); err != nil {
			t.Fatal(err)
		}
	}

	got, err := mgr.ListPlaceTokens(ctx, "payable", workflow.ListOptions{})
	if err != nil {
		t.Fatalf("Manager.ListPlaceTokens: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("payable tokens = %d, want 2 across instances", len(got))
	}
	total := 0.0
	for _, pt := range got {
		v, _ := pt.Token.Get("amount")
		total += v.(float64)
	}
	if total != 30 {
		t.Fatalf("amount total = %v, want 30", total)
	}
}
