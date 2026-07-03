// Command cpn_routing demonstrates guard-based token routing loaded entirely from
// YAML. A batch of orders (colored tokens carrying an amount) is routed to either
// "approved" or "review" purely by per-transition guards that inspect each
// token's own data (token.amount). No routing logic lives in Go — the guards
// decide.
package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"

	wfyaml "github.com/ehabterra/workflow/yaml"
)

//go:embed workflow.yaml
var workflowYAML []byte

func main() {
	ctx := context.Background()

	cfg, err := wfyaml.LoadConfigFromBytes(workflowYAML)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	wf, err := wfyaml.NewLoader().LoadWorkflow(cfg, "orders-2026")
	if err != nil {
		log.Fatalf("load workflow: %v", err)
	}

	fmt.Printf("Received %d orders at 'pending'.\n", wf.TokenCount("pending"))
	fmt.Println("Routing each order — the transition guards decide, not this program:")

	// For each order, try the candidate transitions. ApplyTransitionForToken fires
	// only if that transition's guard accepts the token; a blocked guard returns an
	// error and leaves the token in place, so we fall through to the next option.
	for _, tok := range wf.GetTokens("pending") {
		id, _ := tok.Get("order_id")
		amount, _ := tok.Get("amount")

		switch {
		case wf.ApplyTransitionForToken(ctx, "auto_approve", tok.ID()) == nil:
			fmt.Printf("  %-4v amount %-5v → auto-approved\n", id, amount)
		case wf.ApplyTransitionForToken(ctx, "manual_review", tok.ID()) == nil:
			fmt.Printf("  %-4v amount %-5v → manual review\n", id, amount)
		default:
			fmt.Printf("  %-4v amount %-5v → NOT ROUTED\n", id, amount)
		}
	}

	fmt.Printf("\nResult: approved=%d  review=%d  pending=%d\n",
		wf.TokenCount("approved"), wf.TokenCount("review"), wf.TokenCount("pending"))

	fmt.Println("\nApproved:")
	for _, t := range wf.GetTokens("approved") {
		id, _ := t.Get("order_id")
		fmt.Printf("  - %v\n", id)
	}
	fmt.Println("Needs review:")
	for _, t := range wf.GetTokens("review") {
		id, _ := t.Get("order_id")
		fmt.Printf("  - %v\n", id)
	}
}
