// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
)

// guardedWF builds pending -> approved guarded by `pass == true` in context.
func guardedWF(t *testing.T) *workflow.Workflow {
	t.Helper()
	tr, err := workflow.NewTransition("approve", []workflow.Place{"pending"}, []workflow.Place{"approved"})
	if err != nil {
		t.Fatal(err)
	}
	gc, err := workflow.NewExpressionConstraint("pass == true")
	if err != nil {
		t.Fatal(err)
	}
	tr.AddConstraint(gc)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "approved"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("guarded", def, "pending")
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func TestCanWithContextBranches(t *testing.T) {
	ctx := context.Background()
	wf := guardedWF(t)

	// Empty target => ErrInvalidTransition.
	if err := wf.CanWithContext(ctx, nil); !errors.Is(err, workflow.ErrInvalidTransition) {
		t.Fatalf("Can(nil) = %v, want ErrInvalidTransition", err)
	}
	// Unknown target place => ErrInvalidPlace.
	if err := wf.CanWithContext(ctx, []workflow.Place{"nowhere"}); !errors.Is(err, workflow.ErrInvalidPlace) {
		t.Fatalf("Can(unknown) = %v, want ErrInvalidPlace", err)
	}
	// Guard fails => not allowed.
	wf.SetContext("pass", false)
	if err := wf.CanWithContext(ctx, []workflow.Place{"approved"}); err == nil {
		t.Fatal("Can with failing guard should return an error")
	}
	// Guard passes => allowed.
	wf.SetContext("pass", true)
	if err := wf.CanWithContext(ctx, []workflow.Place{"approved"}); err != nil {
		t.Fatalf("Can with passing guard = %v, want nil", err)
	}
}

func TestApplyWithContextGuardRejectedThenAllowed(t *testing.T) {
	ctx := context.Background()
	wf := guardedWF(t)

	// Rejected: guard false leaves the marking untouched.
	wf.SetContext("pass", false)
	if err := wf.ApplyWithContext(ctx, []workflow.Place{"approved"}); err == nil {
		t.Fatal("Apply with failing guard should error")
	}
	if !wf.Marking().HasPlace("pending") || wf.Marking().HasPlace("approved") {
		t.Fatal("failed apply must not move the marking")
	}

	// Allowed: guard true advances.
	wf.SetContext("pass", true)
	if err := wf.ApplyWithContext(ctx, []workflow.Place{"approved"}); err != nil {
		t.Fatalf("Apply with passing guard = %v", err)
	}
	if !wf.Marking().HasPlace("approved") || wf.Marking().HasPlace("pending") {
		t.Fatal("successful apply should move pending -> approved")
	}

	// Applying again with no enabled transition to the target errors.
	if err := wf.ApplyWithContext(ctx, []workflow.Place{"approved"}); err == nil {
		t.Fatal("Apply from a state with no enabling transition should error")
	}
}

func TestApplyTransitionByNameGuardRejected(t *testing.T) {
	ctx := context.Background()
	wf := guardedWF(t)
	wf.SetContext("pass", false)

	err := wf.ApplyTransitionWithContext(ctx, "approve")
	if !errors.Is(err, workflow.ErrGuardRejected) && err == nil {
		t.Fatalf("guard-rejected named apply = %v, want a rejection error", err)
	}
	if wf.Marking().HasPlace("approved") {
		t.Fatal("guard-rejected apply moved the marking")
	}

	// Unknown transition name errors.
	if err := wf.ApplyTransitionWithContext(ctx, "no-such"); err == nil {
		t.Fatal("applying an unknown transition should error")
	}
}
