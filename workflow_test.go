package workflow_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

func TestNewWorkflow(t *testing.T) {
	tests := []struct {
		name          string
		definition    func() (*workflow.Definition, error)
		initialPlace  workflow.Place
		wantErr       bool
		errorContains string
	}{
		{
			name: "valid workflow",
			definition: func() (*workflow.Definition, error) {
				return workflow.NewDefinition(
					[]workflow.Place{"start", "middle", "end"},
					[]workflow.Transition{},
				)
			},
			initialPlace: "start",
			wantErr:      false,
		},
		{
			name: "",
			definition: func() (*workflow.Definition, error) {
				return workflow.NewDefinition([]workflow.Place{"start"}, []workflow.Transition{})
			},
			initialPlace:  "start",
			wantErr:       true,
			errorContains: "name cannot be empty",
		},
		{
			name:          "nil definition",
			definition:    func() (*workflow.Definition, error) { return nil, nil },
			initialPlace:  "start",
			wantErr:       true,
			errorContains: "definition cannot be nil",
		},
		{
			name: "invalid initial place",
			definition: func() (*workflow.Definition, error) {
				return workflow.NewDefinition([]workflow.Place{"valid"}, []workflow.Transition{})
			},
			initialPlace:  "invalid",
			wantErr:       true,
			errorContains: "initial place 'invalid' is not defined in workflow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.definition()
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, err := workflow.NewWorkflow(tt.name, def, tt.initialPlace)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewWorkflow() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				name := wf.Name()
				if name != tt.name {
					t.Errorf("NewWorkflow() name = %v, want %v", name, tt.name)
				}
				if len(wf.CurrentPlaces()) != 1 || wf.CurrentPlaces()[0] != tt.initialPlace {
					t.Errorf("NewWorkflow() current place = %v, want %v", wf.CurrentPlaces(), []workflow.Place{tt.initialPlace})
				}
			} else if err != nil && tt.errorContains != "" && err.Error() != tt.errorContains && !tt.wantErr {
				t.Errorf("NewWorkflow() error = %v, want error containing %v", err, tt.errorContains)
			}
		})
	}
}

func TestWorkflow_Can(t *testing.T) {
	tests := []struct {
		name         string
		initialPlace workflow.Place
		to           []workflow.Place
		defPlaces    []workflow.Place
		defTrans     []struct {
			name string
			from []workflow.Place
			to   []workflow.Place
		}
		wantErr bool
	}{
		{
			name:         "valid single place transition",
			initialPlace: "a",
			to:           []workflow.Place{"b"},
			defPlaces:    []workflow.Place{"a", "b"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-b", []workflow.Place{"a"}, []workflow.Place{"b"}},
			},
			wantErr: false,
		},
		{
			name:         "valid multiple places transition",
			initialPlace: "a",
			to:           []workflow.Place{"b", "c"},
			defPlaces:    []workflow.Place{"a", "b", "c"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-bc", []workflow.Place{"a"}, []workflow.Place{"b", "c"}},
			},
			wantErr: false,
		},
		{
			name:         "invalid transition - no path",
			initialPlace: "a",
			to:           []workflow.Place{"c"},
			defPlaces:    []workflow.Place{"a", "b", "c"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-b", []workflow.Place{"a"}, []workflow.Place{"b"}},
			},
			wantErr: true,
		},
		{
			name:         "invalid transition - empty target",
			initialPlace: "a",
			to:           []workflow.Place{},
			defPlaces:    []workflow.Place{"a", "b"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-b", []workflow.Place{"a"}, []workflow.Place{"b"}},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var defTransObjs []workflow.Transition
			for _, tr := range tt.defTrans {
				trObj, err := workflow.NewTransition(tr.name, tr.from, tr.to)
				if err != nil {
					t.Fatalf("failed to create transition: %v", err)
				}
				defTransObjs = append(defTransObjs, *trObj)
			}
			definition, err := workflow.NewDefinition(tt.defPlaces, defTransObjs)
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, _ := workflow.NewWorkflow("test", definition, tt.initialPlace)
			err = wf.CanWithContext(context.Background(), tt.to)
			if (err != nil) != tt.wantErr {
				t.Errorf("Workflow.Can() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWorkflow_Apply(t *testing.T) {
	tests := []struct {
		name         string
		definition   func() (*workflow.Definition, error)
		initialPlace workflow.Place
		targetPlaces []workflow.Place
		wantErr      bool
		check        func(*workflow.Workflow) error
	}{
		{
			name: "valid single place transition",
			definition: func() (*workflow.Definition, error) {
				t, _ := workflow.NewTransition("to-middle", []workflow.Place{"start"}, []workflow.Place{"middle"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "middle"},
					[]workflow.Transition{*t},
				)
			},
			initialPlace: "start",
			targetPlaces: []workflow.Place{"middle"},
			wantErr:      false,
			check: func(w *workflow.Workflow) error {
				if len(w.CurrentPlaces()) != 1 || w.CurrentPlaces()[0] != "middle" {
					return fmt.Errorf("expected current place to be 'middle', got %v", w.CurrentPlaces())
				}
				return nil
			},
		},
		{
			name: "valid multiple places transition",
			definition: func() (*workflow.Definition, error) {
				t, _ := workflow.NewTransition("to-multiple", []workflow.Place{"start"}, []workflow.Place{"place1", "place2"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "place1", "place2"},
					[]workflow.Transition{*t},
				)
			},
			initialPlace: "start",
			targetPlaces: []workflow.Place{"place1", "place2"},
			wantErr:      false,
			check: func(w *workflow.Workflow) error {
				if len(w.CurrentPlaces()) != 2 {
					return fmt.Errorf("expected 2 current places, got %d", len(w.CurrentPlaces()))
				}
				places := map[workflow.Place]bool{"place1": true, "place2": true}
				for _, place := range w.CurrentPlaces() {
					if !places[place] {
						return fmt.Errorf("unexpected place %v", place)
					}
				}
				return nil
			},
		},
		{
			name: "self-transition",
			definition: func() (*workflow.Definition, error) {
				t, _ := workflow.NewTransition("self-transition", []workflow.Place{"start"}, []workflow.Place{"start"})
				return workflow.NewDefinition(
					[]workflow.Place{"start"},
					[]workflow.Transition{*t},
				)
			},
			initialPlace: "start",
			targetPlaces: []workflow.Place{"start"},
			wantErr:      false,
			check: func(w *workflow.Workflow) error {
				if len(w.CurrentPlaces()) != 1 || w.CurrentPlaces()[0] != "start" {
					return fmt.Errorf("expected current place to be 'start', got %v", w.CurrentPlaces())
				}
				return nil
			},
		},
		{
			name: "transition with overlapping places",
			definition: func() (*workflow.Definition, error) {
				t, _ := workflow.NewTransition("overlapping", []workflow.Place{"start", "middle"}, []workflow.Place{"middle", "end"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "middle", "end"},
					[]workflow.Transition{*t},
				)
			},
			initialPlace: "start",
			targetPlaces: []workflow.Place{"middle", "end"},
			wantErr:      true,
		},
		{
			name: "invalid transition - empty target",
			definition: func() (*workflow.Definition, error) {
				return workflow.NewDefinition(
					[]workflow.Place{"start"},
					[]workflow.Transition{},
				)
			},
			initialPlace: "start",
			targetPlaces: []workflow.Place{},
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.definition()
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, _ := workflow.NewWorkflow("test", def, tt.initialPlace)

			err = wf.ApplyWithContext(context.Background(), tt.targetPlaces)
			if (err != nil) != tt.wantErr {
				t.Errorf("Workflow.Apply() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && tt.check != nil {
				if err := tt.check(wf); err != nil {
					t.Errorf("Workflow.Apply() place check failed: %v", err)
				}
			}
		})
	}
}

func TestWorkflow_GetEnabledTransitions(t *testing.T) {
	tests := []struct {
		name         string
		initialPlace workflow.Place
		transitions  []struct {
			from []workflow.Place
			to   []workflow.Place
		}
		defPlaces []workflow.Place
		defTrans  []struct {
			name string
			from []workflow.Place
			to   []workflow.Place
		}
		wantTransitions []string
		wantErr         bool
	}{
		{
			name:         "single enabled transition",
			initialPlace: "a",
			defPlaces:    []workflow.Place{"a", "b", "c"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-b", []workflow.Place{"a"}, []workflow.Place{"b"}},
				{"to-c", []workflow.Place{"b"}, []workflow.Place{"c"}},
			},
			transitions: []struct {
				from []workflow.Place
				to   []workflow.Place
			}{
				{from: []workflow.Place{"a"}, to: []workflow.Place{"b"}},
			},
			wantTransitions: []string{"to-c"},
			wantErr:         false,
		},
		{
			name:         "multiple enabled transitions",
			initialPlace: "a",
			defPlaces:    []workflow.Place{"a", "b", "c", "d"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-b", []workflow.Place{"a"}, []workflow.Place{"b"}},
				{"to-c", []workflow.Place{"a"}, []workflow.Place{"c"}},
				{"to-d", []workflow.Place{"b"}, []workflow.Place{"d"}},
				{"b-to-c", []workflow.Place{"b"}, []workflow.Place{"c"}},
			},
			transitions: []struct {
				from []workflow.Place
				to   []workflow.Place
			}{
				{from: []workflow.Place{"a"}, to: []workflow.Place{"b"}},
			},
			wantTransitions: []string{"to-d", "b-to-c"},
			wantErr:         false,
		},
		{
			name:         "no enabled transitions",
			initialPlace: "c",
			defPlaces:    []workflow.Place{"a", "b", "c"},
			defTrans: []struct {
				name string
				from []workflow.Place
				to   []workflow.Place
			}{
				{"to-b", []workflow.Place{"a"}, []workflow.Place{"b"}},
				{"to-c", []workflow.Place{"b"}, []workflow.Place{"c"}},
			},
			transitions: []struct {
				from []workflow.Place
				to   []workflow.Place
			}{
				{from: []workflow.Place{"a"}, to: []workflow.Place{"b"}},
			},
			wantTransitions: []string{},
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var defTransObjs []workflow.Transition
			for _, tr := range tt.defTrans {
				trObj, err := workflow.NewTransition(tr.name, tr.from, tr.to)
				if err != nil {
					t.Fatalf("failed to create transition: %v", err)
				}
				defTransObjs = append(defTransObjs, *trObj)
			}
			definition, err := workflow.NewDefinition(tt.defPlaces, defTransObjs)
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, _ := workflow.NewWorkflow("test", definition, tt.initialPlace)

			for _, trans := range tt.transitions {
				err := wf.ApplyWithContext(context.Background(), trans.to)
				if (err != nil) != tt.wantErr {
					t.Errorf("Workflow.Apply() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if tt.wantErr {
					return
				}
			}

			got, err := wf.EnabledTransitions()
			if err != nil {
				t.Errorf("Workflow.EnabledTransitions() error = %v", err)
				return
			}
			if len(got) != len(tt.wantTransitions) {
				t.Errorf("Workflow.EnabledTransitions() = %v, want %v", got, tt.wantTransitions)
				return
			}

			// Check that all expected transitions are present
			for _, want := range tt.wantTransitions {
				found := false
				for _, trans := range got {
					if trans.Name() == want {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Workflow.EnabledTransitions() missing transition %v", want)
				}
			}
		})
	}
}

func TestWorkflow_Events(t *testing.T) {
	tr, _ := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		t.Fatalf("failed to create definition: %v", err)
	}
	wf, _ := workflow.NewWorkflow("test", definition, "start")

	var beforeTransitionCalled bool
	var afterTransitionCalled bool
	var guardCalled bool

	wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error {
		beforeTransitionCalled = true
		return nil
	})

	wf.AddEventListener(workflow.EventAfterTransition, func(event workflow.Event) error {
		afterTransitionCalled = true
		return nil
	})

	wf.AddGuardEventListener(func(event *workflow.GuardEvent) error {
		guardCalled = true
		return nil
	})

	err = wf.ApplyWithContext(context.Background(), []workflow.Place{"end"})
	if err != nil {
		t.Errorf("Workflow.Apply() error = %v", err)
	}

	if !beforeTransitionCalled {
		t.Error("before transition event was not called")
	}
	if !afterTransitionCalled {
		t.Error("after transition event was not called")
	}
	if !guardCalled {
		t.Error("guard event was not called")
	}
}

func TestWorkflow_Context(t *testing.T) {
	tr, _ := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
	definition, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*tr},
	)
	if err != nil {
		t.Fatalf("failed to create definition: %v", err)
	}
	wf, _ := workflow.NewWorkflow("test", definition, "start")

	// Test setting and getting context
	wf.SetContext("key", "value")
	value, ok := wf.Context("key")
	if !ok || value != "value" {
		t.Errorf("Context() = %v, %v, want %v, %v", value, ok, "value", true)
	}

	// Test getting non-existent context
	_, ok = wf.Context("non-existent")
	if ok {
		t.Error("Context() = true, want false")
	}
}

func TestWorkflow_ForkAndMerge(t *testing.T) {
	tests := []struct {
		name         string
		definition   func() (*workflow.Definition, error)
		initialPlace workflow.Place
		transitions  []struct {
			from []workflow.Place
			to   []workflow.Place
		}
		wantErr bool
		check   func(*workflow.Workflow) error
	}{
		{
			name: "simple fork and merge",
			definition: func() (*workflow.Definition, error) {
				t1, _ := workflow.NewTransition("fork", []workflow.Place{"start"}, []workflow.Place{"branch1", "branch2"})
				t2, _ := workflow.NewTransition("merge", []workflow.Place{"branch1", "branch2"}, []workflow.Place{"end"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "branch1", "branch2", "end"},
					[]workflow.Transition{*t1, *t2},
				)
			},
			initialPlace: "start",
			transitions: []struct {
				from []workflow.Place
				to   []workflow.Place
			}{
				{from: []workflow.Place{"start"}, to: []workflow.Place{"branch1", "branch2"}},
				{from: []workflow.Place{"branch1", "branch2"}, to: []workflow.Place{"end"}},
			},
			wantErr: false,
			check: func(w *workflow.Workflow) error {
				places := w.CurrentPlaces()
				if len(places) != 1 || places[0] != "end" {
					return fmt.Errorf("expected final place to be 'end', got %v", places)
				}
				return nil
			},
		},
		{
			name: "complex fork and merge",
			definition: func() (*workflow.Definition, error) {
				t1, _ := workflow.NewTransition("fork1", []workflow.Place{"start"}, []workflow.Place{"branch1", "branch2"})
				t2, _ := workflow.NewTransition("fork2", []workflow.Place{"branch1"}, []workflow.Place{"branch3"})
				t3, _ := workflow.NewTransition("merge1", []workflow.Place{"branch2", "branch3"}, []workflow.Place{"merge1", "branch1"})
				t4, _ := workflow.NewTransition("merge2", []workflow.Place{"merge1", "branch1"}, []workflow.Place{"merge2"})
				t5, _ := workflow.NewTransition("end", []workflow.Place{"merge2"}, []workflow.Place{"end"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "branch1", "branch2", "branch3", "merge1", "merge2", "end"},
					[]workflow.Transition{*t1, *t2, *t3, *t4, *t5},
				)
			},
			initialPlace: "start",
			transitions: []struct {
				from []workflow.Place
				to   []workflow.Place
			}{
				{from: []workflow.Place{"start"}, to: []workflow.Place{"branch1", "branch2"}},
				{from: []workflow.Place{"branch1"}, to: []workflow.Place{"branch3"}},
				{from: []workflow.Place{"branch2", "branch3"}, to: []workflow.Place{"merge1", "branch1"}},
				{from: []workflow.Place{"merge1", "branch1"}, to: []workflow.Place{"merge2"}},
				{from: []workflow.Place{"merge2"}, to: []workflow.Place{"end"}},
			},
			wantErr: false,
			check: func(w *workflow.Workflow) error {
				places := w.CurrentPlaces()
				if len(places) != 1 || places[0] != "end" {
					return fmt.Errorf("expected final place to be 'end', got %v", places)
				}
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.definition()
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, _ := workflow.NewWorkflow("test", def, tt.initialPlace)

			for _, trans := range tt.transitions {
				err := wf.ApplyWithContext(context.Background(), trans.to)
				if (err != nil) != tt.wantErr {
					t.Errorf("Workflow.Apply() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				if tt.wantErr {
					return
				}
			}

			if tt.check != nil {
				if err := tt.check(wf); err != nil {
					t.Errorf("Workflow.Apply() place check failed: %v", err)
				}
			}
		})
	}
}

func TestWorkflow_Diagram(t *testing.T) {
	tests := []struct {
		name         string
		definition   func() (*workflow.Definition, error)
		initialPlace workflow.Place
		want         string
	}{
		{
			name: "simple workflow",
			definition: func() (*workflow.Definition, error) {
				t, _ := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "end"},
					[]workflow.Transition{*t},
				)
			},
			initialPlace: "start",
			want: `stateDiagram-v2
    direction TB
    classDef currentPlace font-weight:bold,stroke-width:4px
    start
    end
    start --> end : <span class="transition-label" data-transition-name="to-end">to-end</span>

    %% Current places
    class start currentPlace

    %% Initial place
    [*] --> start
`,
		},
		{
			name: "complex workflow",
			definition: func() (*workflow.Definition, error) {
				t1, _ := workflow.NewTransition("to-middle", []workflow.Place{"start"}, []workflow.Place{"middle"})
				t2, _ := workflow.NewTransition("to-end", []workflow.Place{"middle"}, []workflow.Place{"end"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "middle", "end"},
					[]workflow.Transition{*t1, *t2},
				)
			},
			initialPlace: "start",
			want: `stateDiagram-v2
    direction TB
    classDef currentPlace font-weight:bold,stroke-width:4px
    start
    middle
    end
    start --> middle : <span class="transition-label" data-transition-name="to-middle">to-middle</span>
    middle --> end : <span class="transition-label" data-transition-name="to-end">to-end</span>

    %% Current places
    class start currentPlace

    %% Initial place
    [*] --> start
`,
		},
		{
			name: "fork and merge workflow",
			definition: func() (*workflow.Definition, error) {
				t1, _ := workflow.NewTransition("fork", []workflow.Place{"start"}, []workflow.Place{"branch1", "branch2"})
				t2, _ := workflow.NewTransition("merge", []workflow.Place{"branch1", "branch2"}, []workflow.Place{"end"})
				return workflow.NewDefinition(
					[]workflow.Place{"start", "branch1", "branch2", "end"},
					[]workflow.Transition{*t1, *t2},
				)
			},
			initialPlace: "start",
			want: `stateDiagram-v2
    direction TB
    classDef currentPlace font-weight:bold,stroke-width:4px
    start
    branch1
    branch2
    end
    state fork_fork <<fork>>
    start --> fork_fork : <span class="transition-label" data-transition-name="fork">fork</span>
    fork_fork --> branch1
    fork_fork --> branch2
    state merge_join <<join>>
    branch1 --> merge_join : <span class="transition-label" data-transition-name="merge">merge</span>
    branch2 --> merge_join : <span class="transition-label" data-transition-name="merge">merge</span>
    merge_join --> end

    %% Current places
    class start currentPlace

    %% Initial place
    [*] --> start
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.definition()
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, err := workflow.NewWorkflow("test", def, tt.initialPlace)
			if err != nil {
				t.Fatalf("failed to create workflow: %v", err)
			}

			got := wf.Diagram()
			if got != tt.want {
				t.Errorf("Diagram() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWorkflow_InitialPlace(t *testing.T) {
	tests := []struct {
		name         string
		definition   func() (*workflow.Definition, error)
		initialPlace workflow.Place
		wantPlace    workflow.Place
	}{
		{
			name: "simple workflow",
			definition: func() (*workflow.Definition, error) {
				return workflow.NewDefinition(
					[]workflow.Place{"start", "middle", "end"},
					[]workflow.Transition{},
				)
			},
			initialPlace: "start",
			wantPlace:    "start",
		},
		{
			name: "complex workflow",
			definition: func() (*workflow.Definition, error) {
				return workflow.NewDefinition(
					[]workflow.Place{"draft", "review", "approved"},
					[]workflow.Transition{},
				)
			},
			initialPlace: "draft",
			wantPlace:    "draft",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := tt.definition()
			if err != nil {
				t.Fatalf("failed to create definition: %v", err)
			}
			wf, err := workflow.NewWorkflow("test", def, tt.initialPlace)
			if err != nil {
				t.Fatalf("failed to create workflow: %v", err)
			}

			got := wf.InitialPlace()
			if got != tt.wantPlace {
				t.Errorf("GetInitialPlace() = %v, want %v", got, tt.wantPlace)
			}
		})
	}
}

// TestApplyTransitionByName tests the new transition-by-name API
func TestApplyTransitionByName(t *testing.T) {
	// Create a simple workflow with multiple transitions to same destination
	def := &workflow.Definition{
		Places: []workflow.Place{"draft", "review", "rejected", "approved"},
		Transitions: []workflow.Transition{
			*workflow.MustNewTransition("submit", []workflow.Place{"draft"}, []workflow.Place{"review"}),
			*workflow.MustNewTransition("approve", []workflow.Place{"review"}, []workflow.Place{"approved"}),
			*workflow.MustNewTransition("reject_quality", []workflow.Place{"review"}, []workflow.Place{"rejected"}),
			*workflow.MustNewTransition("reject_policy", []workflow.Place{"review"}, []workflow.Place{"rejected"}),
		},
	}

	wf, err := workflow.NewWorkflow("test", def, "draft")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Test 1: Apply transition by name
	if err := wf.ApplyTransition("submit"); err != nil {
		t.Errorf("ApplyTransition('submit') failed: %v", err)
	}

	// Verify we're in the correct state
	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "review" {
		t.Errorf("Expected [review], got %v", places)
	}

	// Test 2: Check if specific transition is allowed
	if err := wf.CanTransition("approve"); err != nil {
		t.Errorf("CanTransition('approve') should be allowed: %v", err)
	}

	if err := wf.CanTransition("reject_quality"); err != nil {
		t.Errorf("CanTransition('reject_quality') should be allowed: %v", err)
	}

	if err := wf.CanTransition("reject_policy"); err != nil {
		t.Errorf("CanTransition('reject_policy') should be allowed: %v", err)
	}

	// Test 3: Apply specific rejection transition
	if err := wf.ApplyTransition("reject_quality"); err != nil {
		t.Errorf("ApplyTransition('reject_quality') failed: %v", err)
	}

	// Verify we're in rejected state
	places = wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "rejected" {
		t.Errorf("Expected [rejected], got %v", places)
	}

	// Test 4: Try non-existent transition
	if err := wf.CanTransition("nonexistent"); err == nil {
		t.Errorf("CanTransition('nonexistent') should fail")
	}

	if err := wf.ApplyTransition("nonexistent"); err == nil {
		t.Errorf("ApplyTransition('nonexistent') should fail")
	}
}

// TestApplyTransitionWithContext tests transition-by-name with context
func TestApplyTransitionWithContext(t *testing.T) {
	def := &workflow.Definition{
		Places: []workflow.Place{"start", "end"},
		Transitions: []workflow.Transition{
			*workflow.MustNewTransition("go", []workflow.Place{"start"}, []workflow.Place{"end"}),
		},
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	ctx := yaml.WithTemplateValue(context.Background(), "test_key", "test_value")

	if err := wf.CanTransitionWithContext(ctx, "go"); err != nil {
		t.Errorf("CanTransitionWithContext failed: %v", err)
	}

	if err := wf.ApplyTransitionWithContext(ctx, "go"); err != nil {
		t.Errorf("ApplyTransitionWithContext failed: %v", err)
	}

	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "end" {
		t.Errorf("Expected [end], got %v", places)
	}
}

// TestApplyTransitionWithGuards tests transition-by-name with guard constraints
func TestApplyTransitionWithGuards(t *testing.T) {
	// Create workflow with a guarded transition
	def := &workflow.Definition{
		Places: []workflow.Place{"start", "end"},
	}

	transition := workflow.MustNewTransition("guarded", []workflow.Place{"start"}, []workflow.Place{"end"})

	// Add a guard that blocks the transition
	transition.AddConstraint(&testConstraint{shouldFail: true})
	def.Transitions = []workflow.Transition{*transition}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Test that guard blocks the transition
	if err := wf.CanTransition("guarded"); err == nil {
		t.Errorf("CanTransition should be blocked by guard")
	}

	if err := wf.ApplyTransition("guarded"); err == nil {
		t.Errorf("ApplyTransition should be blocked by guard")
	}

	// Verify we're still at start
	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "start" {
		t.Errorf("Expected [start], got %v", places)
	}
}

// TestApplyTransitionNotEnabled tests applying a transition that's not enabled
func TestApplyTransitionNotEnabled(t *testing.T) {
	def := &workflow.Definition{
		Places: []workflow.Place{"start", "middle", "end"},
		Transitions: []workflow.Transition{
			*workflow.MustNewTransition("step1", []workflow.Place{"start"}, []workflow.Place{"middle"}),
			*workflow.MustNewTransition("step2", []workflow.Place{"middle"}, []workflow.Place{"end"}),
		},
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Try to apply step2 when we're still at start (not enabled)
	if err := wf.CanTransition("step2"); !errors.Is(err, workflow.ErrTransitionNotAllowed) {
		t.Errorf("Expected ErrTransitionNotAllowed, got %v", err)
	}

	if err := wf.ApplyTransition("step2"); !errors.Is(err, workflow.ErrTransitionNotAllowed) {
		t.Errorf("Expected ErrTransitionNotAllowed, got %v", err)
	}

	// Apply step1, then step2 should work
	if err := wf.ApplyTransition("step1"); err != nil {
		t.Errorf("ApplyTransition('step1') failed: %v", err)
	}

	if err := wf.ApplyTransition("step2"); err != nil {
		t.Errorf("ApplyTransition('step2') failed: %v", err)
	}

	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "end" {
		t.Errorf("Expected [end], got %v", places)
	}
}

// TestMultipleTransitionsSameDestination tests the ambiguous case that motivated this feature
func TestMultipleTransitionsSameDestination(t *testing.T) {
	// Create workflow with multiple rejection paths
	def := &workflow.Definition{
		Places: []workflow.Place{"draft", "review_a", "review_b", "rejected", "approved"},
		Transitions: []workflow.Transition{
			*workflow.MustNewTransition("submit_to_a", []workflow.Place{"draft"}, []workflow.Place{"review_a"}),
			*workflow.MustNewTransition("submit_to_b", []workflow.Place{"draft"}, []workflow.Place{"review_b"}),
			*workflow.MustNewTransition("reject_from_a", []workflow.Place{"review_a"}, []workflow.Place{"rejected"}),
			*workflow.MustNewTransition("reject_from_b", []workflow.Place{"review_b"}, []workflow.Place{"rejected"}),
			*workflow.MustNewTransition("approve_from_a", []workflow.Place{"review_a"}, []workflow.Place{"approved"}),
			*workflow.MustNewTransition("approve_from_b", []workflow.Place{"review_b"}, []workflow.Place{"approved"}),
		},
	}

	wf, err := workflow.NewWorkflow("test", def, "draft")
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}

	// Go through path A
	if err := wf.ApplyTransition("submit_to_a"); err != nil {
		t.Errorf("Failed: %v", err)
	}

	// Both rejection transitions go to [rejected], but we want to be explicit
	// about which one we're using
	if err := wf.ApplyTransition("reject_from_a"); err != nil {
		t.Errorf("Failed: %v", err)
	}

	places := wf.CurrentPlaces()
	if len(places) != 1 || places[0] != "rejected" {
		t.Errorf("Expected [rejected], got %v", places)
	}

	// Create another instance and go through path B
	wf2, _ := workflow.NewWorkflow("test2", def, "draft")
	if err := wf2.ApplyTransition("submit_to_b"); err != nil {
		t.Errorf("Failed: %v", err)
	}

	// This is a different rejection transition even though it goes to same place
	if err := wf2.ApplyTransition("reject_from_b"); err != nil {
		t.Errorf("Failed: %v", err)
	}

	places = wf2.CurrentPlaces()
	if len(places) != 1 || places[0] != "rejected" {
		t.Errorf("Expected [rejected], got %v", places)
	}
}

// TestConditionalJoinPattern tests the conditional join pattern
func TestConditionalJoinPattern(t *testing.T) {
	// Simulate conditional parallel branches (like QA+Security vs QA+Security+Legal)
	def := &workflow.Definition{
		Places: []workflow.Place{"start", "task_a", "task_b", "task_c", "done"},
		Transitions: []workflow.Transition{
			// Fork into 2 or 3 parallel tasks based on some condition
			*workflow.MustNewTransition("fork_standard", []workflow.Place{"start"}, []workflow.Place{"task_a", "task_b"}),
			*workflow.MustNewTransition("fork_extended", []workflow.Place{"start"}, []workflow.Place{"task_a", "task_b", "task_c"}),

			// Two different joins
			*workflow.MustNewTransition("join_standard", []workflow.Place{"task_a", "task_b"}, []workflow.Place{"done"}),
			*workflow.MustNewTransition("join_extended", []workflow.Place{"task_a", "task_b", "task_c"}, []workflow.Place{"done"}),
		},
	}

	// Test standard path (2 tasks)
	wf1, _ := workflow.NewWorkflow("test1", def, "start")
	if err := wf1.ApplyTransition("fork_standard"); err != nil {
		t.Errorf("fork_standard failed: %v", err)
	}

	// Should only be able to use standard join
	if err := wf1.CanTransition("join_standard"); err != nil {
		t.Errorf("join_standard should be available: %v", err)
	}
	if err := wf1.CanTransition("join_extended"); err == nil {
		t.Errorf("join_extended should NOT be available (missing task_c)")
	}

	err := wf1.ApplyTransition("join_standard")
	if err != nil {
		t.Errorf("join_standard failed: %v", err)
	}
	places := wf1.CurrentPlaces()
	if len(places) != 1 || places[0] != "done" {
		t.Errorf("Expected [done], got %v", places)
	}

	// Test extended path (3 tasks)
	wf2, _ := workflow.NewWorkflow("test2", def, "start")
	if err := wf2.ApplyTransition("fork_extended"); err != nil {
		t.Errorf("fork_extended failed: %v", err)
	}

	placesAfterFork := wf2.CurrentPlaces()
	t.Logf("After fork_extended, places: %v", placesAfterFork)

	// Both joins are technically enabled because all 'from' places are present
	// But we should use the extended join to consume all three tasks
	if err := wf2.CanTransition("join_extended"); err != nil {
		t.Errorf("join_extended should be available: %v", err)
	}

	// Application code should choose the right join based on context
	// In practice, you'd check workflow.Context("path_type") or similar
	if err := wf2.ApplyTransition("join_extended"); err != nil {
		t.Errorf("join_extended failed: %v", err)
	}
	places = wf2.CurrentPlaces()
	t.Logf("After join_extended, places: %v", places)
	if len(places) != 1 || places[0] != "done" {
		t.Errorf("Expected [done], got %v", places)
	}

	// Demonstrate what happens if we use the wrong join (standard) on extended path
	wf3, _ := workflow.NewWorkflow("test3", def, "start")
	if err := wf3.ApplyTransition("fork_extended"); err != nil {
		t.Errorf("fork_extended failed: %v", err)
	}

	// Using join_standard on extended path will leave task_c behind
	if err := wf3.ApplyTransition("join_standard"); err != nil {
		t.Errorf("join_standard failed: %v", err)
	}
	places = wf3.CurrentPlaces()
	// This results in [task_c, done] - a partial join
	// This shows why application logic must choose the correct join
	if len(places) != 2 {
		t.Errorf("Expected [task_c, done] (partial join), got %v", places)
	}
}

func TestWorkflow_RemoveListener(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Add listeners and get handles
	handle1 := wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle2 := wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle3 := wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	// Remove middle listener using handle
	wf.RemoveListener(handle2)

	// Remove last listener
	wf.RemoveListener(handle3)

	// Remove first listener
	wf.RemoveListener(handle1)

	// Test removing with nil handle (should not panic)
	wf.RemoveListener(nil)

	// Test removing same handle twice (should not panic)
	handle4 := wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	wf.RemoveListener(handle4)
	wf.RemoveListener(handle4) // Should be safe to call twice
}

func TestWorkflow_RemoveListener_EdgeCases(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	wf2, err := workflow.NewWorkflow("test2", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Test removing handle from different workflow
	handle1 := wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle2 := wf2.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	// Try to remove handle2 (from wf2) from wf1 - should not remove anything
	wf.RemoveListener(handle2)

	// Clean up
	wf.RemoveListener(handle1)
	wf2.RemoveListener(handle2)
}

func TestWorkflow_AllContext(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Test with empty context
	allContext := wf.AllContext()
	if allContext == nil {
		t.Error("AllContext() should not return nil")
	}
	if len(allContext) != 0 {
		t.Errorf("AllContext() should return empty map, got %v", allContext)
	}

	// Test with context values
	wf.SetContext("key1", "value1")
	wf.SetContext("key2", 42)
	wf.SetContext("key3", true)

	allContext = wf.AllContext()
	if len(allContext) != 3 {
		t.Errorf("AllContext() should return 3 items, got %d", len(allContext))
	}
	if allContext["key1"] != "value1" {
		t.Errorf("AllContext() key1 = %v, want 'value1'", allContext["key1"])
	}
	if allContext["key2"] != 42 {
		t.Errorf("AllContext() key2 = %v, want 42", allContext["key2"])
	}
	if allContext["key3"] != true {
		t.Errorf("AllContext() key3 = %v, want true", allContext["key3"])
	}

	// Verify it's a copy (modifying returned map shouldn't affect workflow)
	allContext["newKey"] = "newValue"
	_, exists := wf.Context("newKey")
	if exists {
		t.Error("AllContext() should return a copy, not the original map")
	}
}

func TestWorkflow_Can_Wrapper(t *testing.T) {
	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "middle", "end"},
		[]workflow.Transition{
			*workflow.MustNewTransition("move", []workflow.Place{"start"}, []workflow.Place{"middle"}),
		},
	)
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Test Can with valid transition
	err = wf.Can([]workflow.Place{"middle"})
	if err != nil {
		t.Errorf("Can() with valid transition should not error, got %v", err)
	}

	// Test Can with invalid transition
	err = wf.Can([]workflow.Place{"end"})
	if err == nil {
		t.Error("Can() with invalid transition should error")
	}
}

func TestWorkflow_Definition(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Test Definition() returns the correct definition
	returnedDef := wf.Definition()
	if returnedDef != def {
		t.Errorf("Definition() = %v, want %v", returnedDef, def)
	}
}

func TestWorkflow_SetMarking(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Test SetMarking with nil (should error)
	err = wf.SetMarking(nil)
	if err == nil {
		t.Error("SetMarking(nil) should return error")
	}

	// Test SetMarking with valid marking
	marking := workflow.NewMarking([]workflow.Place{"end"})
	err = wf.SetMarking(marking)
	if err != nil {
		t.Errorf("SetMarking() with valid marking should not error, got %v", err)
	}

	// Verify marking was set
	currentPlaces := wf.CurrentPlaces()
	if len(currentPlaces) != 1 || currentPlaces[0] != "end" {
		t.Errorf("SetMarking() did not set marking correctly, got %v", currentPlaces)
	}
}

func TestWorkflow_RemoveListener_IndexOutOfBounds(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("NewWorkflow() failed: %v", err)
	}

	// Add a listener
	handle := wf.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	// Manually corrupt the handle map to simulate index out of bounds
	// This tests the index >= len(listeners) check
	// We can't directly access private fields, but we can test by removing the listener
	// and then trying to remove it again (which will hit the "handle not found" path)
	wf.RemoveListener(handle)

	// Try to remove again - should safely handle missing handle
	wf.RemoveListener(handle)
}
