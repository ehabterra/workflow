// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"strconv"
	"testing"

	"github.com/ehabterra/workflow"
)

func BenchmarkNewWorkflow(b *testing.B) {
	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "middle", "end"},
		[]workflow.Transition{},
	)
	if err != nil {
		b.Fatalf("failed to create definition: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := workflow.NewWorkflow("test", definition, "start")
		if err != nil {
			b.Fatalf("failed to create workflow: %v", err)
		}
	}
}

func BenchmarkWorkflow_Apply(b *testing.B) {
	tr, err := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
	if err != nil {
		b.Fatalf("failed to create transition: %v", err)
	}

	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		b.Fatalf("failed to create definition: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", definition, "start")
	if err != nil {
		b.Fatalf("failed to create workflow: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := wf.Apply([]workflow.Place{"end"})
		if err != nil {
			b.Fatalf("failed to apply transition: %v", err)
		}
		// Reset the workflow place for the next iteration
		if err := wf.SetMarking(workflow.NewMarking([]workflow.Place{"start"})); err != nil {
			b.Fatalf("failed to set marking: %v", err)
		}
	}
}

func BenchmarkWorkflow_GetEnabledTransitions(b *testing.B) {
	tr, err := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
	if err != nil {
		b.Fatalf("failed to create transition: %v", err)
	}

	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		b.Fatalf("failed to create definition: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", definition, "start")
	if err != nil {
		b.Fatalf("failed to create workflow: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := wf.EnabledTransitions()
		if err != nil {
			b.Fatalf("failed to get enabled transitions: %v", err)
		}
	}
}

func BenchmarkWorkflow_Events(b *testing.B) {
	tr, err := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
	if err != nil {
		b.Fatalf("failed to create transition: %v", err)
	}

	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		b.Fatalf("failed to create definition: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", definition, "start")
	if err != nil {
		b.Fatalf("failed to create workflow: %v", err)
	}

	// Add event listeners
	wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error {
		return nil
	})
	wf.AddEventListener(workflow.EventAfterTransition, func(event workflow.Event) error {
		return nil
	})
	wf.AddGuardEventListener(func(event *workflow.GuardEvent) error {
		return nil
	})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := wf.Apply([]workflow.Place{"end"})
		if err != nil {
			b.Fatalf("failed to apply transition: %v", err)
		}
		// Reset the workflow place for the next iteration
		if err := wf.SetMarking(workflow.NewMarking([]workflow.Place{"start"})); err != nil {
			b.Fatalf("failed to set marking: %v", err)
		}
	}
}

func BenchmarkRegistry_Operations(b *testing.B) {
	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{},
	)
	if err != nil {
		b.Fatalf("failed to create definition: %v", err)
	}

	b.Run("AddWorkflow", func(b *testing.B) {
		registry := workflow.NewRegistry()

		for i := 0; i < b.N; i++ {
			wf, err := workflow.NewWorkflow("test"+strconv.Itoa(i), definition, "start")
			if err != nil {
				b.Fatalf("failed to create workflow: %v", err)
			}
			err = registry.AddWorkflow(wf)
			if err != nil {
				b.Fatalf("failed to add workflow: %v", err)
			}
		}
	})

	b.Run("GetWorkflow", func(b *testing.B) {
		registry := workflow.NewRegistry()

		wf, err := workflow.NewWorkflow("test", definition, "start")
		if err != nil {
			b.Fatalf("failed to create workflow: %v", err)
		}
		err = registry.AddWorkflow(wf)
		if err != nil {
			b.Fatalf("failed to add workflow: %v", err)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := registry.Workflow("test")
			if err != nil {
				b.Fatalf("failed to get workflow: %v", err)
			}
		}
	})

	b.Run("ListWorkflows", func(b *testing.B) {
		registry := workflow.NewRegistry()

		wf, err := workflow.NewWorkflow("test", definition, "start")
		if err != nil {
			b.Fatalf("failed to create workflow: %v", err)
		}
		err = registry.AddWorkflow(wf)
		if err != nil {
			b.Fatalf("failed to add workflow: %v", err)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = registry.ListWorkflows()
		}
	})
}
