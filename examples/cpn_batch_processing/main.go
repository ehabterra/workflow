// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

// Command cpn_batch_processing demonstrates Colored Petri Net (CPN) features of
// the workflow engine: data-carrying tokens, whole-batch and per-token firing,
// and token queries / aggregation / transformation.
//
// The workflow models a batch of orders flowing pending -> processing -> done,
// where each order is a token carrying its own data (id, amount).
package main

import (
	"context"
	"fmt"

	"github.com/ehabterra/workflow"
)

func mustTransition(name string, from, to []workflow.Place) workflow.Transition {
	tr, err := workflow.NewTransition(name, from, to)
	if err != nil {
		panic(err)
	}
	return *tr
}

func main() {
	ctx := context.Background()

	def, err := workflow.NewDefinition(
		[]workflow.Place{"pending", "processing", "done"},
		[]workflow.Transition{
			mustTransition("start", []workflow.Place{"pending"}, []workflow.Place{"processing"}),
			mustTransition("finish", []workflow.Place{"processing"}, []workflow.Place{"done"}),
		},
	)
	if err != nil {
		panic(err)
	}

	wf, err := workflow.NewWorkflow("batch", def, "pending")
	if err != nil {
		panic(err)
	}

	// Seed the batch: each order is a colored token. ClearPlace drops the initial
	// presence token so "pending" holds exactly our orders.
	wf.ClearPlace("pending")
	if _, err := wf.CreateTokens("pending", []workflow.TokenData{
		{"id": "A", "amount": 100.0},
		{"id": "B", "amount": 250.0},
		{"id": "C", "amount": 40.0},
	}); err != nil {
		panic(err)
	}
	fmt.Printf("Seeded %d orders at 'pending'\n", wf.TokenCount("pending"))

	// Query + aggregate before doing any work.
	agg := wf.AggregateTokens(nil, "amount")
	fmt.Printf("Batch total = %.0f, avg = %.2f, max = %.0f\n", agg.Sum, agg.Avg, agg.Max)

	// Apply a 10% surcharge to high-value orders (>= 100), keeping their identity.
	surcharged := wf.TransformTokens("pending",
		func(t workflow.Token) bool {
			v, ok := t.Get("amount")
			f, isFloat := v.(float64)
			return ok && isFloat && f >= 100
		},
		func(t workflow.Token) workflow.TokenData {
			d := t.Data()
			if f, ok := d["amount"].(float64); ok {
				d["amount"] = f * 1.1
			}
			return d
		},
	)
	fmt.Printf("Applied surcharge to %d high-value orders; new total = %.0f\n",
		surcharged, wf.AggregateTokens(nil, "amount").Sum)

	// Per-token firing: advance just order "A" from pending to processing.
	var orderA workflow.TokenID
	for _, t := range wf.GetTokens("pending") {
		if id, _ := t.Get("id"); id == "A" {
			orderA = t.ID()
		}
	}
	if err := wf.ApplyTransitionForToken(ctx, "start", orderA); err != nil {
		panic(err)
	}
	fmt.Printf("Advanced order A individually: pending=%d processing=%d\n",
		wf.TokenCount("pending"), wf.TokenCount("processing"))

	// Whole-batch firing: advance the remaining pending orders at once.
	if err := wf.ApplyTransition("start"); err != nil {
		panic(err)
	}
	fmt.Printf("Advanced the rest: pending=%d processing=%d\n",
		wf.TokenCount("pending"), wf.TokenCount("processing"))

	// Finish everything.
	if err := wf.ApplyTransition("finish"); err != nil {
		panic(err)
	}
	fmt.Printf("Finished: processing=%d done=%d\n",
		wf.TokenCount("processing"), wf.TokenCount("done"))

	fmt.Println("Completed orders:")
	for _, t := range wf.GetTokens("done") {
		id, _ := t.Get("id")
		amount, _ := t.Get("amount")
		fmt.Printf("  - %v: %.0f\n", id, amount)
	}
}
