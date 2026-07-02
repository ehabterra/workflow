package yaml_test

import (
	"testing"

	"github.com/ehabterra/workflow/yaml"
)

const cpnYAML = `
workflow:
  name: batch_orders
  initial_place: pending
  places:
    - name: pending
    - name: processing
    - name: done
  initial_tokens:
    pending:
      - order_id: "001"
        amount: 100
      - order_id: "002"
        amount: 250
  transitions:
    - name: start
      from: [pending]
      to: [processing]
    - name: finish
      from: [processing]
      to: [done]
`

func TestLoadWorkflow_InitialTokens(t *testing.T) {
	cfg, err := yaml.LoadConfigFromBytes([]byte(cpnYAML))
	if err != nil {
		t.Fatalf("LoadConfigFromBytes: %v", err)
	}

	wf, err := yaml.NewLoader().LoadWorkflow(cfg, "batch-1")
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}

	// The initial presence token is cleared, so pending holds exactly the two
	// declared colored tokens.
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

func TestValidate_InitialTokensUndefinedPlace(t *testing.T) {
	const bad = `
workflow:
  name: bad
  initial_place: a
  places:
    - name: a
    - name: b
  initial_tokens:
    nope:
      - x: 1
  transitions:
    - name: t
      from: [a]
      to: [b]
`
	if _, err := yaml.LoadConfigFromBytes([]byte(bad)); err == nil {
		t.Fatal("expected error for initial_tokens referencing undefined place")
	}
}

func TestLoadConfig_UnknownKeyStillRejected(t *testing.T) {
	const bad = `
workflow:
  name: bad
  initial_place: a
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
