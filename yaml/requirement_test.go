// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

const approvalYAML = `
workflow:
  name: requisition_approval
  initial_marking: submitted
  places:
    - name: submitted
    - name: approvals
    - name: approved
  transitions:
    - name: approve_final
      from: [submitted, approvals]
      to: [approved]
      resets: [approvals]
      require:
        - place: approvals
          where: "token.role in required_roles"
          distinct: role
          count: "len(required_roles)"
    - name: approve_partial
      from: [submitted]
      to: [submitted]
`

// TestRequireFromYAML: the whole chain-satisfaction pattern is declarative —
// the host records approvals as tokens and asks the net, it does not compute
// whether the chain is satisfied.
func TestRequireFromYAML(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(approvalYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "req-1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	wf.SetContext("required_roles", []any{"manager", "finance"})

	reqs := wf.Definition().Transition("approve_final").Requirements()
	if len(reqs) != 1 {
		t.Fatalf("require not wired, got %v", reqs)
	}
	if got := reqs[0].Spec(); got.Place != "approvals" || got.Distinct != "role" || got.Count != "len(required_roles)" {
		t.Fatalf("requirement mis-decoded: %+v", got)
	}

	record := func(role string) {
		t.Helper()
		if _, err := wf.CreateToken("approvals", workflow.TokenData{"role": role}); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	record("manager")
	fired, err := wf.ApplyAny(ctx, "approve_final", "approve_partial")
	if err != nil {
		t.Fatal(err)
	}
	if fired != "approve_partial" {
		t.Fatalf("one of two roles: want approve_partial, got %q", fired)
	}

	record("finance")
	fired, err = wf.ApplyAny(ctx, "approve_final", "approve_partial")
	if err != nil {
		t.Fatal(err)
	}
	if fired != "approve_final" {
		t.Fatalf("chain satisfied: want approve_final, got %q", fired)
	}
	if places := wf.Marking().Places(); len(places) != 1 || places[0] != "approved" {
		t.Fatalf("want [approved], got %v", places)
	}
}

// TestRequireCountAcceptsBareInteger: a fixed arity should not have to be
// quoted just because the field also accepts an expression.
func TestRequireCountAcceptsBareInteger(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(`
workflow:
  name: batch
  initial_marking: pool
  transitions:
    - name: ship
      from: [pool]
      to: [shipped]
      require:
        - place: pool
          count: 3
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "b1")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := wf.Definition().Transition("ship").Requirements()[0].Spec().Count; got != "3" {
		t.Fatalf("want count \"3\", got %q", got)
	}
}

func TestRequireValidation(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want string
	}{
		"undefined place": {
			yaml: `
workflow:
  name: w
  initial_marking: a
  places:
    - name: a
    - name: b
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - place: nowhere
          count: 1
`,
			want: "undefined place",
		},
		"place is not an input": {
			yaml: `
workflow:
  name: w
  initial_marking: a
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - place: b
          count: 1
`,
			want: "not one of its 'from' places",
		},
		"missing count": {
			yaml: `
workflow:
  name: w
  initial_marking: a
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - place: a
`,
			want: "has no count",
		},
		"missing place": {
			yaml: `
workflow:
  name: w
  initial_marking: a
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - count: 1
`,
			want: "has no place",
		},
		"unknown key": {
			yaml: `
workflow:
  name: w
  initial_marking: a
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - place: a
          cnt: 1
`,
			want: "field cnt not found",
		},
		"count is neither expression nor integer": {
			yaml: `
workflow:
  name: w
  initial_marking: a
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - place: a
          count: [1, 2]
`,
			want: "expression string or an integer",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := yaml.LoadConfigFromBytes([]byte(tc.yaml))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

// TestRequireExpressionCompiledAtLoad: a malformed count must fail the load,
// not the first firing of the branch that uses it.
func TestRequireExpressionCompiledAtLoad(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(`
workflow:
  name: w
  initial_marking: a
  transitions:
    - name: t
      from: [a]
      to: [b]
      require:
        - place: a
          count: "len("
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = yaml.NewLoader().LoadDefinition(cfg)
	if !errors.Is(err, workflow.ErrInvalidExpression) {
		t.Fatalf("want ErrInvalidExpression at build time, got %v", err)
	}
}
