// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/yaml"
	_ "github.com/mattn/go-sqlite3"
)

func newHistoryStore(t *testing.T) history.HistoryStore {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	h := history.NewSQLiteHistory(db)
	if err := h.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	return h
}

func twoStepWF(t *testing.T) *workflow.Workflow {
	t.Helper()
	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"start"}, []workflow.Place{"end"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("hist", def, "start")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestApplyTransitionByNameWithHistoryHappyPath(t *testing.T) {
	hs := newHistoryStore(t)
	wf := twoStepWF(t)
	// Actor is drawn from the context via getStringFromContext.
	ctx := yaml.WithTemplateValue(context.Background(), "actor", "alice")

	if err := yaml.ApplyTransitionByNameWithHistory(wf, "go", hs, ctx, "", "", nil); err != nil {
		t.Fatalf("apply-with-history = %v", err)
	}
	recs, err := hs.ListHistory(ctx, "hist", history.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Actor != "alice" {
		t.Fatalf("history = %#v, want one record actor=alice", recs)
	}
}

func TestApplyTransitionByNameWithHistoryErrors(t *testing.T) {
	hs := newHistoryStore(t)
	ctx := context.Background()

	// Unknown transition name.
	wf := twoStepWF(t)
	if err := yaml.ApplyTransitionByNameWithHistory(wf, "missing", hs, ctx, "", "", nil); err == nil {
		t.Fatal("unknown transition should error")
	}

	// Transition not enabled from the current marking (already at end).
	wf2 := twoStepWF(t)
	if err := wf2.ApplyTransitionWithContext(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	if err := yaml.ApplyTransitionByNameWithHistory(wf2, "go", hs, ctx, "", "", nil); err == nil {
		t.Fatal("applying a disabled transition should error")
	}

	// Empty marking: no current places.
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := workflow.NewWorkflowFromMarking("empty", def, workflow.NewMarking(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.ApplyTransitionByNameWithHistory(empty, "go", hs, ctx, "", "", nil); err == nil {
		t.Fatal("apply on an empty marking should report no current places")
	}
}

func TestSaveTransitionHistoryMetadataMergeAndError(t *testing.T) {
	ctx := context.Background()

	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"start"}, []workflow.Place{"end"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Notes and actor default from transition metadata; a templated custom field
	// resolves from context.
	tr := def.Transition("go")
	tr.SetMetadata("history_notes", "meta notes")
	tr.SetMetadata("history_actor", "meta actor")
	tr.SetMetadata("history_custom_fields", map[string]any{"reason": "{{reason}}"})

	// History store with the custom columns the metadata/context supply.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	hs := history.NewSQLiteHistory(db, history.WithCustomFields(map[string]string{
		"reason": "reason TEXT",
		"extra":  "extra TEXT",
	}))
	if err := hs.Initialize(ctx); err != nil {
		t.Fatal(err)
	}

	wf, err := workflow.NewWorkflow("hist2", def, "start")
	if err != nil {
		t.Fatal(err)
	}
	// Context supplies the template value AND a merged custom field, both via the
	// package's own string-keyed helper (the mechanism the resolver reads from).
	tctx := yaml.WithTemplateValue(ctx, "reason", "audit")
	tctx = yaml.WithTemplateValue(tctx, "custom_fields", map[string]any{"extra": "ctxval"})

	if err := yaml.ApplyTransitionByNameWithHistory(wf, "go", hs, tctx, "", "", map[string]any{"extra": "override"}); err != nil {
		t.Fatalf("apply-with-history = %v", err)
	}
	recs, err := hs.ListHistory(tctx, "hist2", history.QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Notes != "meta notes" || recs[0].Actor != "meta actor" {
		t.Fatalf("record = %#v, want meta notes/actor", recs)
	}

	// History save error: applying to a store whose DB is closed returns an error
	// even though the in-memory transition itself succeeded.
	def2, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*workflow.MustNewTransition("go", []workflow.Place{"start"}, []workflow.Place{"end"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf2, err := workflow.NewWorkflow("hist3", def2, "start")
	if err != nil {
		t.Fatal(err)
	}
	db2, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	hs2 := history.NewSQLiteHistory(db2)
	if err := hs2.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := yaml.ApplyTransitionByNameWithHistory(wf2, "go", hs2, ctx, "n", "a", nil); err == nil {
		t.Fatal("history save on a closed DB should return an error")
	}
}

func TestStorageConfigToMapNil(t *testing.T) {
	var sc *yaml.StorageConfig
	if sc.ToMap() != nil {
		t.Fatal("(*StorageConfig)(nil).ToMap() should be nil")
	}
}

func TestInitialMarkingForms(t *testing.T) {
	// Sequence form.
	seq := "workflow:\n  name: w\n  initial_marking: [a, b]\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(seq)); err != nil {
		t.Fatalf("sequence initial_marking = %v", err)
	}

	// Mapping form with colored tokens.
	mapForm := "workflow:\n  name: w\n  initial_marking:\n    a:\n      - amount: 100\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(mapForm)); err != nil {
		t.Fatalf("mapping initial_marking = %v", err)
	}

	// Omitted initial_marking is allowed (pure token-pool net).
	none := "workflow:\n  name: w\n  places:\n    - name: a\n    - name: b\n  transitions:\n    - name: t\n      from: [a]\n      to: [b]\n"
	if _, err := yaml.LoadConfigFromBytes([]byte(none)); err != nil {
		t.Fatalf("omitted initial_marking = %v", err)
	}
}
