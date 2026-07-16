// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package yaml_test

import (
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

// initial_marking in its map form seeds colored tokens per place.
const cpnYAML = `
workflow:
  name: batch_orders
  initial_marking:
    pending:
      - order_id: "001"
        amount: 100
      - order_id: "002"
        amount: 250
  places:
    - name: pending
    - name: processing
    - name: done
  transitions:
    - name: start
      from: [pending]
      to: [processing]
    - name: finish
      from: [processing]
      to: [done]
`

func TestLoadWorkflow_InitialMarkingTokens(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(cpnYAML))
	if err != nil {
		t.Fatalf("LoadConfigFromBytes: %v", err)
	}

	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "batch-1")
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}

	// pending holds exactly the two declared colored tokens (no phantom).
	if got := wf.TokenCount("pending"); got != 2 {
		t.Fatalf("pending token count = %d, want 2", got)
	}

	amounts := map[string]any{}
	for _, tok := range wf.GetTokens("pending") {
		order, _ := tok.Get("order_id")
		amount, _ := tok.Get("amount")
		amounts[order.(string)] = amount
	}
	if amounts["001"] != 100 || amounts["002"] != 250 {
		t.Fatalf("initial token data wrong: %+v", amounts)
	}

	// Tokens flow through a transition end-to-end.
	if err := wf.ApplyTransition("start"); err != nil {
		t.Fatalf("ApplyTransition(start): %v", err)
	}
	if got := wf.TokenCount("processing"); got != 2 {
		t.Fatalf("processing token count = %d, want 2 after start", got)
	}
	if got := wf.TokenCount("pending"); got != 0 {
		t.Fatalf("pending token count = %d, want 0 after start", got)
	}
}

// initial_marking in its scalar form is the boolean shorthand: one presence token.
func TestLoadWorkflow_InitialMarkingScalar(t *testing.T) {
	const y = `
workflow:
  name: article
  initial_marking: draft
  transitions:
    - {name: submit,  from: [draft],  to: [review]}
    - {name: publish, from: [review], to: [published]}
`
	cfg, err := yaml.LoadConfigFromBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadConfigFromBytes: %v", err)
	}
	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "a-1")
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if !wf.Marking().HasPlace("draft") {
		t.Fatalf("expected draft to be the initial place, got %v", wf.CurrentPlaces())
	}
	if wf.InitialPlace() != "draft" {
		t.Fatalf("InitialPlace() = %q, want draft", wf.InitialPlace())
	}
}

// initial_marking in its list form marks several presence places.
func TestLoadWorkflow_InitialMarkingList(t *testing.T) {
	const y = `
workflow:
  name: parallel
  initial_marking: [design, legal]
  places: [{name: design}, {name: legal}, {name: done}]
  transitions:
    - {name: finish_design, from: [design], to: [done]}
    - {name: finish_legal,  from: [legal],  to: [done]}
`
	cfg, err := yaml.LoadConfigFromBytes([]byte(y))
	if err != nil {
		t.Fatalf("LoadConfigFromBytes: %v", err)
	}
	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "p-1")
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if got := wf.InitialPlaces(); len(got) != 2 {
		t.Fatalf("InitialPlaces() = %v, want 2 places", got)
	}
	if !wf.Marking().HasPlace("design") || !wf.Marking().HasPlace("legal") {
		t.Fatalf("expected both design and legal marked: %v", wf.CurrentPlaces())
	}
}

func TestValidate_InitialMarkingUndefinedPlace(t *testing.T) {
	const bad = `
workflow:
  name: bad
  initial_marking:
    nope:
      - x: 1
  places:
    - name: a
    - name: b
  transitions:
    - name: t
      from: [a]
      to: [b]
`
	if _, err := yaml.LoadConfigFromBytes([]byte(bad)); err == nil {
		t.Fatal("expected error for initial_marking referencing undefined place")
	}
}

// initial_marking may be omitted entirely: a pure token-pool net starts with
// an EMPTY marking (every place legitimately empty between batches) and only
// fires once tokens arrive.
func TestLoadWorkflow_NoInitialMarkingStartsEmpty(t *testing.T) {
	const pool = `
workflow:
  name: payment_pool
  transitions:
    - {name: pay, from: [payable], to: [paid_out]}
`
	cfg, err := yaml.LoadConfigFromBytes([]byte(pool))
	if err != nil {
		t.Fatalf("a pool net without initial_marking must be valid, got: %v", err)
	}

	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "pool-1")
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if places := wf.Marking().Places(); len(places) != 0 {
		t.Fatalf("expected an empty starting marking, got %v", places)
	}

	// The empty pool is inert until a token arrives, then fires normally.
	if _, err := wf.CreateToken("payable", workflow.TokenData{"amount": 10.0}); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if err := wf.ApplyTransition("pay"); err != nil {
		t.Fatalf("ApplyTransition(pay): %v", err)
	}
	if got := wf.TokenCount("paid_out"); got != 1 {
		t.Fatalf("paid_out token count = %d, want 1", got)
	}
}

func TestLoadConfig_UnknownKeyStillRejected(t *testing.T) {
	const bad = `
workflow:
  name: bad
  initial_marking: a
  cpn_enabled: true
  transitions:
    - name: t
      from: [a]
      to: [b]
`
	if _, err := yaml.LoadConfigFromBytes([]byte(bad)); err == nil {
		t.Fatal("expected strict decoding to reject the unknown cpn_enabled key")
	}
}
