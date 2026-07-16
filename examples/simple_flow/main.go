// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"

	"github.com/ehabterra/workflow"
)

func main() {
	// Create a workflow definition
	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "middle", "end"},
		[]workflow.Transition{
			func() workflow.Transition {
				tr, _ := workflow.NewTransition("to-middle", []workflow.Place{"start"}, []workflow.Place{"middle"})
				return *tr
			}(),
			func() workflow.Transition {
				tr, _ := workflow.NewTransition("to-end", []workflow.Place{"middle"}, []workflow.Place{"end"})
				return *tr
			}(),
		},
	)
	if err != nil {
		panic(err)
	}

	// Create a new workflow
	wf, err := workflow.NewWorkflow("simple-flow", definition, "start")
	if err != nil {
		panic(err)
	}

	// Print initial place
	fmt.Printf("Initial place: %s\n", wf.InitialPlace())

	// Add event listeners
	wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error {
		fmt.Printf("Before transition: %s\n", event.Transition().Name())
		return nil
	})

	wf.AddEventListener(workflow.EventAfterTransition, func(event workflow.Event) error {
		fmt.Printf("After transition: %s\n", event.Transition().Name())
		return nil
	})

	// Apply transitions
	err = wf.Apply([]workflow.Place{"middle"})
	if err != nil {
		panic(err)
	}

	err = wf.Apply([]workflow.Place{"end"})
	if err != nil {
		panic(err)
	}

	// Print the workflow diagram
	fmt.Println("\nWorkflow Diagram:")
	fmt.Println(wf.Diagram())
}
