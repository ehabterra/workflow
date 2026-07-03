package main

import (
	"context"
	"testing"

	wfyaml "github.com/ehabterra/workflow/yaml"
)

// TestGuardRouting verifies the end-to-end guard-based routing: each order token
// advances along the transition whose guard its amount satisfies.
func TestGuardRouting(t *testing.T) {
	ctx := context.Background()

	cfg, err := wfyaml.LoadConfigFromBytes(workflowYAML)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	wf, err := wfyaml.NewLoader().LoadWorkflow(cfg, "test")
	if err != nil {
		t.Fatalf("load workflow: %v", err)
	}

	for _, tok := range wf.GetTokens("pending") {
		if wf.ApplyTransitionForToken(ctx, "auto_approve", tok.ID()) == nil {
			continue
		}
		if err := wf.ApplyTransitionForToken(ctx, "manual_review", tok.ID()); err != nil {
			id, _ := tok.Get("order_id")
			t.Fatalf("order %v was not routed by any guard: %v", id, err)
		}
	}

	// A-1 (500) and A-3 (250) auto-approve; A-2 (1500) and A-4 (9000) go to review.
	if got := wf.TokenCount("approved"); got != 2 {
		t.Fatalf("approved = %d, want 2", got)
	}
	if got := wf.TokenCount("review"); got != 2 {
		t.Fatalf("review = %d, want 2", got)
	}
	if got := wf.TokenCount("pending"); got != 0 {
		t.Fatalf("pending = %d, want 0", got)
	}
}
