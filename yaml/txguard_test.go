// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

const txGuardYAML = `
workflow:
  name: doc
  initial_marking: draft
  transitions:
    - name: publish
      from: [draft]
      to: [published]
      guard: "hasRole('editor')"
      tx_guard: "approvedCount() >= 2"
`

// TestTxGuardFromYAML: both guards wire up, and the tx-scoped one is recorded
// as structure so it reaches the fingerprint and the diagram.
func TestTxGuardFromYAML(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(txGuardYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	loader := yaml.NewLoaderWithTxEnv(func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		return map[string]any{"approvedCount": func() int { return 0 }}
	})
	def, err := loader.LoadDefinition(cfg)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	tr := def.Transition("publish")
	if v, ok := tr.Metadata("tx_guard"); !ok || v != "approvedCount() >= 2" {
		t.Fatalf("tx_guard metadata = %v (ok=%v)", v, ok)
	}
	if v, ok := tr.Metadata("guard"); !ok || v != "hasRole('editor')" {
		t.Fatalf("the plain guard must survive alongside it, got %v (ok=%v)", v, ok)
	}
	if !strings.Contains(def.Diagram(), "approvedCount()") {
		t.Error("the diagram should show the tx guard")
	}

	// Both are structural: changing either moves the fingerprint.
	other, err := yaml.LoadConfigFromBytes([]byte(
		strings.Replace(txGuardYAML, ">= 2", ">= 3", 1)))
	if err != nil {
		t.Fatal(err)
	}
	otherDef, err := loader.LoadDefinition(other)
	if err != nil {
		t.Fatal(err)
	}
	if def.Fingerprint() == otherDef.Fingerprint() {
		t.Error("changing the tx_guard expression must move the fingerprint")
	}
}

// TestTxGuardWithoutABuilderIsRejectedAtLoad: a tx_guard has nothing to call
// without a TxEnvBuilder, so the failure belongs at load, not at the first
// firing of the branch that uses it.
func TestTxGuardWithoutABuilderIsRejectedAtLoad(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(txGuardYAML))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = yaml.NewLoader().LoadDefinition(cfg)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "NewLoaderWithTxEnv") {
		t.Fatalf("the error should say how to fix it, got %v", err)
	}
}

// TestTxGuardMalformedExpressionIsRejectedAtLoad mirrors the plain-guard rule.
func TestTxGuardMalformedExpressionIsRejectedAtLoad(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(`
workflow:
  name: doc
  initial_marking: draft
  transitions:
    - name: publish
      from: [draft]
      to: [published]
      tx_guard: "len("
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	loader := yaml.NewLoaderWithTxEnv(func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		return nil
	})
	if _, err := loader.LoadDefinition(cfg); err == nil {
		t.Fatal("an uncompilable tx_guard must fail the load")
	}
}
