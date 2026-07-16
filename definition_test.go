// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow_test

import (
	"testing"

	"github.com/ehabterra/workflow"
)

func TestNewDefinition(t *testing.T) {
	tests := []struct {
		name        string
		places      []workflow.Place
		transitions []workflow.Transition
		wantErr     bool
		errContains string
	}{
		{
			name:   "valid definition",
			places: []workflow.Place{"start", "end"},
			transitions: []workflow.Transition{
				func() workflow.Transition {
					t, _ := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
					return *t
				}(),
			},
			wantErr: false,
		},
		{
			name:   "invalid transition - missing from place",
			places: []workflow.Place{"end"},
			transitions: []workflow.Transition{
				func() workflow.Transition {
					t, _ := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
					return *t
				}(),
			},
			wantErr:     true,
			errContains: "place 'start' in transition 'to-end' is not defined in workflow places",
		},
		{
			name:   "invalid transition - missing to place",
			places: []workflow.Place{"start"},
			transitions: []workflow.Transition{
				func() workflow.Transition {
					t, _ := workflow.NewTransition("to-end", []workflow.Place{"start"}, []workflow.Place{"end"})
					return *t
				}(),
			},
			wantErr:     true,
			errContains: "place 'end' in transition 'to-end' is not defined in workflow places",
		},
		{
			name:   "invalid fork - missing place",
			places: []workflow.Place{"start", "branch1", "end"},
			transitions: []workflow.Transition{
				func() workflow.Transition {
					t, _ := workflow.NewTransition("fork", []workflow.Place{"start"}, []workflow.Place{"branch1", "non-existent"})
					return *t
				}(),
			},
			wantErr:     true,
			errContains: "place 'non-existent' in transition 'fork' is not defined in workflow places",
		},
		{
			name:   "invalid merge - missing place",
			places: []workflow.Place{"start", "branch1", "branch2", "end"},
			transitions: []workflow.Transition{
				func() workflow.Transition {
					t1, _ := workflow.NewTransition("fork", []workflow.Place{"start"}, []workflow.Place{"branch1", "branch2"})
					return *t1
				}(),
				func() workflow.Transition {
					t2, _ := workflow.NewTransition("merge", []workflow.Place{"branch1", "non-existent"}, []workflow.Place{"end"})
					return *t2
				}(),
			},
			wantErr:     true,
			errContains: "place 'non-existent' in transition 'merge' is not defined in workflow places",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := workflow.NewDefinition(tt.places, tt.transitions)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDefinition() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errContains != "" && err.Error() != tt.errContains {
				t.Errorf("NewDefinition() error = %v, want error containing %v", err, tt.errContains)
			}
		})
	}
}

func TestDefinition_AllPlaces(t *testing.T) {
	tests := []struct {
		name   string
		places []workflow.Place
	}{
		{
			name:   "empty places",
			places: []workflow.Place{},
		},
		{
			name:   "single place",
			places: []workflow.Place{"start"},
		},
		{
			name:   "multiple places",
			places: []workflow.Place{"start", "middle", "end"},
		},
		{
			name:   "many places",
			places: []workflow.Place{"p1", "p2", "p3", "p4", "p5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def, err := workflow.NewDefinition(tt.places, []workflow.Transition{})
			if err != nil {
				t.Fatalf("NewDefinition() failed: %v", err)
			}

			result := def.AllPlaces()
			if len(result) != len(tt.places) {
				t.Errorf("AllPlaces() length = %d, want %d", len(result), len(tt.places))
			}

			// Verify it's a copy, not the same slice
			if len(tt.places) > 0 {
				if &result[0] == &tt.places[0] {
					t.Error("AllPlaces() returned same slice, want copy")
				}
			}

			// Verify contents match
			for i, p := range tt.places {
				if result[i] != p {
					t.Errorf("AllPlaces()[%d] = %v, want %v", i, result[i], p)
				}
			}

			// Modify result and verify original is unchanged
			if len(result) > 0 {
				originalFirst := tt.places[0]
				result[0] = "modified"
				if tt.places[0] != originalFirst {
					t.Error("AllPlaces() modification affected original slice")
				}
			}
		})
	}
}

func TestDefinition_AllTransitions(t *testing.T) {
	tests := []struct {
		name        string
		transitions []workflow.Transition
	}{
		{
			name:        "empty transitions",
			transitions: []workflow.Transition{},
		},
		{
			name: "single transition",
			transitions: []workflow.Transition{
				*workflow.MustNewTransition("t1", []workflow.Place{"start"}, []workflow.Place{"end"}),
			},
		},
		{
			name: "multiple transitions",
			transitions: []workflow.Transition{
				*workflow.MustNewTransition("t1", []workflow.Place{"start"}, []workflow.Place{"middle"}),
				*workflow.MustNewTransition("t2", []workflow.Place{"middle"}, []workflow.Place{"end"}),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			places := []workflow.Place{"start", "middle", "end"}
			def, err := workflow.NewDefinition(places, tt.transitions)
			if err != nil {
				t.Fatalf("NewDefinition() failed: %v", err)
			}

			result := def.AllTransitions()
			if len(result) != len(tt.transitions) {
				t.Errorf("AllTransitions() length = %d, want %d", len(result), len(tt.transitions))
			}

			// Verify it's a copy, not the same slice
			if len(tt.transitions) > 0 {
				if &result[0] == &tt.transitions[0] {
					t.Error("AllTransitions() returned same slice, want copy")
				}
			}

			// Verify contents match
			for i, tr := range tt.transitions {
				if result[i].Name() != tr.Name() {
					t.Errorf("AllTransitions()[%d].Name() = %v, want %v", i, result[i].Name(), tr.Name())
				}
			}
		})
	}
}

func TestDefinition_Transition(t *testing.T) {
	tr1 := workflow.MustNewTransition("transition1", []workflow.Place{"start"}, []workflow.Place{"end"})
	tr2 := workflow.MustNewTransition("transition2", []workflow.Place{"start"}, []workflow.Place{"middle"})
	tr3 := workflow.MustNewTransition("transition3", []workflow.Place{"middle"}, []workflow.Place{"end"})

	places := []workflow.Place{"start", "middle", "end"}
	transitions := []workflow.Transition{*tr1, *tr2, *tr3}
	def, err := workflow.NewDefinition(places, transitions)
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	tests := []struct {
		name      string
		trName    string
		wantFound bool
		wantName  string
	}{
		{
			name:      "find first transition",
			trName:    "transition1",
			wantFound: true,
			wantName:  "transition1",
		},
		{
			name:      "find middle transition",
			trName:    "transition2",
			wantFound: true,
			wantName:  "transition2",
		},
		{
			name:      "find last transition",
			trName:    "transition3",
			wantFound: true,
			wantName:  "transition3",
		},
		{
			name:      "non-existent transition",
			trName:    "non-existent",
			wantFound: false,
		},
		{
			name:      "empty string",
			trName:    "",
			wantFound: false,
		},
		{
			name:      "case sensitive",
			trName:    "Transition1",
			wantFound: false,
		},
		{
			name:      "partial match",
			trName:    "transition",
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := def.Transition(tt.trName)
			if (result != nil) != tt.wantFound {
				t.Errorf("Transition(%q) = %v, want found = %v", tt.trName, result != nil, tt.wantFound)
				return
			}
			if tt.wantFound && result != nil {
				if result.Name() != tt.wantName {
					t.Errorf("Transition(%q).Name() = %v, want %v", tt.trName, result.Name(), tt.wantName)
				}
			}
		})
	}
}

func TestDefinition_AddEventListener(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	// Test adding listener when Listeners is nil
	listener1 := func(event workflow.Event) error { return nil }
	def.AddEventListener(workflow.EventBeforeTransition, listener1)

	if def.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("AddEventListener() listener count = %d, want 1", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test adding multiple listeners
	listener2 := func(event workflow.Event) error { return nil }
	def.AddEventListener(workflow.EventBeforeTransition, listener2)

	if def.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("AddEventListener() listener count = %d, want 2", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test adding listener for different event type
	listener3 := func(event workflow.Event) error { return nil }
	def.AddEventListener(workflow.EventAfterTransition, listener3)

	if def.ListenerCount(workflow.EventAfterTransition) != 1 {
		t.Errorf("AddEventListener() listener count for EventAfterTransition = %d, want 1", def.ListenerCount(workflow.EventAfterTransition))
	}
	if def.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("AddEventListener() listener count for EventBeforeTransition = %d, want 2", def.ListenerCount(workflow.EventBeforeTransition))
	}
}

func TestDefinition_AddGuardEventListener(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	// Test adding guard listener when Listeners is nil
	guardListener1 := func(event *workflow.GuardEvent) error { return nil }
	def.AddGuardEventListener(guardListener1)

	if def.ListenerCount(workflow.EventGuard) != 1 {
		t.Errorf("AddGuardEventListener() listener count = %d, want 1", def.ListenerCount(workflow.EventGuard))
	}

	// Test adding multiple guard listeners
	guardListener2 := func(event *workflow.GuardEvent) error { return nil }
	def.AddGuardEventListener(guardListener2)

	if def.ListenerCount(workflow.EventGuard) != 2 {
		t.Errorf("AddGuardEventListener() listener count = %d, want 2", def.ListenerCount(workflow.EventGuard))
	}
}

func TestDefinition_RemoveListener(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	// Add listeners and get handles
	handle1 := def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle2 := def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle3 := def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	if def.ListenerCount(workflow.EventBeforeTransition) != 3 {
		t.Fatalf("Expected 3 listeners, got %d", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove middle listener using handle
	def.RemoveListener(handle2)

	if def.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove last listener
	def.RemoveListener(handle3)

	if def.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("RemoveListener() listener count = %d, want 1", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove first listener
	def.RemoveListener(handle1)

	if def.ListenerCount(workflow.EventBeforeTransition) != 0 {
		t.Errorf("RemoveListener() listener count = %d, want 0", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test removing with nil handle (should not panic)
	def.RemoveListener(nil)

	// Test removing same handle twice (should not panic)
	handle4 := def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	def.RemoveListener(handle4)
	def.RemoveListener(handle4) // Should be safe to call twice
}

func TestDefinition_RemoveEventListener(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	// Test removing when Listeners is nil (should not panic)
	def.RemoveListener(def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil }))

	// Add listeners
	listener1 := func(event workflow.Event) error { return nil }
	listener2 := func(event workflow.Event) error { return nil }
	listener3 := func(event workflow.Event) error { return nil }

	handle1 := def.AddEventListener(workflow.EventBeforeTransition, listener1)
	handle2 := def.AddEventListener(workflow.EventBeforeTransition, listener2)
	handle3 := def.AddEventListener(workflow.EventBeforeTransition, listener3)

	if def.ListenerCount(workflow.EventBeforeTransition) != 3 {
		t.Fatalf("Expected 3 listeners, got %d", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove middle listener using handle
	def.RemoveListener(handle2)

	if def.ListenerCount(workflow.EventBeforeTransition) != 2 {
		t.Errorf("RemoveListener() listener count = %d, want 2", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove last listener
	def.RemoveListener(handle3)

	if def.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("RemoveListener() listener count = %d, want 1", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Remove first listener
	def.RemoveListener(handle1)

	if def.ListenerCount(workflow.EventBeforeTransition) != 0 {
		t.Errorf("RemoveListener() listener count = %d, want 0", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Test removing with nil handle (should not panic)
	def.RemoveListener(nil)

	// Test removing same handle twice (should not panic)
	handle4 := def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	def.RemoveListener(handle4)
	def.RemoveListener(handle4) // Should be safe to call twice
}

func TestDefinition_RemoveListener_EdgeCases(t *testing.T) {
	def, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	// Test removing handle from different definition
	def2, err := workflow.NewDefinition([]workflow.Place{"start", "end"}, []workflow.Transition{})
	if err != nil {
		t.Fatalf("NewDefinition() failed: %v", err)
	}

	handle1 := def.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })
	handle2 := def2.AddEventListener(workflow.EventBeforeTransition, func(event workflow.Event) error { return nil })

	// Try to remove handle2 (from def2) from def1 - should not remove anything
	def.RemoveListener(handle2)
	if def.ListenerCount(workflow.EventBeforeTransition) != 1 {
		t.Errorf("RemoveListener() with wrong owner should not remove listener, got %d", def.ListenerCount(workflow.EventBeforeTransition))
	}

	// Clean up
	def.RemoveListener(handle1)
	def2.RemoveListener(handle2)
}
