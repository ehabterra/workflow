// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

func TestResolveTemplateValueForms(t *testing.T) {
	ctx := context.Background()

	// Non-string values pass through untouched.
	if got := yaml.ResolveTemplateValue(42, ctx, nil); got != 42 {
		t.Fatalf("non-string = %v, want 42", got)
	}

	// A literal string with no template markers is returned as-is.
	if got := yaml.ResolveTemplateValue("plain text", ctx, nil); got != "plain text" {
		t.Fatalf("literal = %v, want 'plain text'", got)
	}

	// now() resolves to a timestamp string.
	if got := yaml.ResolveTemplateValue("now()", ctx, nil); got == "now()" || got == "" {
		t.Fatalf("now() did not resolve: %v", got)
	}

	// Simple variable from the request context (string key via WithTemplateValue).
	ctx2 := yaml.WithTemplateValue(ctx, "reason", "late")
	if got := yaml.ResolveTemplateValue("because {{reason}}", ctx2, nil); got != "because late" {
		t.Fatalf("simple ctx var = %v, want 'because late'", got)
	}

	// Nested property from a map[string]any in context.
	ctx3 := yaml.WithTemplateValue(ctx, "request", map[string]any{"ip": "10.0.0.1"})
	if got := yaml.ResolveTemplateValue("from {{request.ip}}", ctx3, nil); got != "from 10.0.0.1" {
		t.Fatalf("nested string-map = %v, want 'from 10.0.0.1'", got)
	}

	// Nested property from a map[any]any (YAML-decoded shape).
	ctx4 := yaml.WithTemplateValue(ctx, "obj", map[any]any{"k": "v"})
	if got := yaml.ResolveTemplateValue("{{obj.k}}", ctx4, nil); got != "v" {
		t.Fatalf("nested any-map = %v, want 'v'", got)
	}

	// A missing variable resolves to empty.
	if got := yaml.ResolveTemplateValue("x{{nope}}y", ctx, nil); got != "xy" {
		t.Fatalf("missing var = %v, want 'xy'", got)
	}
	// A nested lookup whose root object is absent also resolves to empty.
	if got := yaml.ResolveTemplateValue("[{{missing.field}}]", ctx, nil); got != "[]" {
		t.Fatalf("missing nested root = %v, want '[]'", got)
	}
}

func TestResolveTemplateValueFromWorkflowContext(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"a", "b"},
		[]workflow.Transition{*workflow.MustNewTransition("t", []workflow.Place{"a"}, []workflow.Place{"b"})},
	)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := workflow.NewWorkflow("w", def, "a")
	if err != nil {
		t.Fatal(err)
	}
	wf.SetContext("owner", "alice")
	wf.SetContext("meta", map[string]any{"team": "finance"})

	ctx := context.Background()
	// Simple variable falling through to the workflow context.
	if got := yaml.ResolveTemplateValue("{{owner}}", ctx, wf); got != "alice" {
		t.Fatalf("wf simple var = %v, want 'alice'", got)
	}
	// Nested property from the workflow context.
	if got := yaml.ResolveTemplateValue("{{meta.team}}", ctx, wf); got != "finance" {
		t.Fatalf("wf nested var = %v, want 'finance'", got)
	}
	// Nested lookup on an object that is not a map returns empty.
	wf.SetContext("scalar", "x")
	if got := yaml.ResolveTemplateValue("{{scalar.field}}", ctx, wf); got != "" {
		t.Fatalf("nested on scalar = %v, want ''", got)
	}
}
