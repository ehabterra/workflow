package workflow_test

import (
	"context"
	"testing"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/yaml"
)

func TestBaseEvent_Context(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		wantNil bool
	}{
		{
			name:    "with context",
			ctx:     context.Background(),
			wantNil: false,
		},
		{
			name:    "with context with value",
			ctx:     yaml.WithTemplateValue(context.Background(), "key", "value"),
			wantNil: false,
		},
		{
			name:    "with TODO context",
			ctx:     context.TODO(),
			wantNil: false,
		},
		{
			name:    "nil context",
			ctx:     nil,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			event := workflow.NewEvent(tt.ctx, workflow.EventBeforeTransition, tr, []workflow.Place{"start"}, []workflow.Place{"end"}, nil)

			result := event.Context()
			if (result == nil) != tt.wantNil {
				t.Errorf("Context() = %v, want nil = %v", result, tt.wantNil)
			}

			if !tt.wantNil && result != tt.ctx {
				t.Errorf("Context() = %v, want %v", result, tt.ctx)
			}
		})
	}
}

func TestGuardEvent_SetBlocking(t *testing.T) {
	tests := []struct {
		name            string
		initialBlocking bool
		setBlocking     bool
		wantBlocking    bool
	}{
		{
			name:            "set to true from false",
			initialBlocking: false,
			setBlocking:     true,
			wantBlocking:    true,
		},
		{
			name:            "set to false from true",
			initialBlocking: true,
			setBlocking:     false,
			wantBlocking:    false,
		},
		{
			name:            "set to true from true",
			initialBlocking: true,
			setBlocking:     true,
			wantBlocking:    true,
		},
		{
			name:            "set to false from false",
			initialBlocking: false,
			setBlocking:     false,
			wantBlocking:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			event := workflow.NewGuardEvent(context.Background(), tr, []workflow.Place{"start"}, []workflow.Place{"end"}, nil)

			// Set initial blocking state
			event.SetBlocking(tt.initialBlocking)
			if event.IsBlocking() != tt.initialBlocking {
				t.Errorf("Initial IsBlocking() = %v, want %v", event.IsBlocking(), tt.initialBlocking)
			}

			// Set new blocking state
			event.SetBlocking(tt.setBlocking)
			if event.IsBlocking() != tt.wantBlocking {
				t.Errorf("IsBlocking() after SetBlocking(%v) = %v, want %v", tt.setBlocking, event.IsBlocking(), tt.wantBlocking)
			}
		})
	}
}

func TestGuardEvent_IsBlocking(t *testing.T) {
	tr := workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
	event := workflow.NewGuardEvent(context.Background(), tr, []workflow.Place{"start"}, []workflow.Place{"end"}, nil)

	// Default should be false
	if event.IsBlocking() != false {
		t.Errorf("IsBlocking() = %v, want false", event.IsBlocking())
	}

	// Set to true
	event.SetBlocking(true)
	if event.IsBlocking() != true {
		t.Errorf("IsBlocking() after SetBlocking(true) = %v, want true", event.IsBlocking())
	}

	// Set to false
	event.SetBlocking(false)
	if event.IsBlocking() != false {
		t.Errorf("IsBlocking() after SetBlocking(false) = %v, want false", event.IsBlocking())
	}
}

func TestNewEvent(t *testing.T) {
	tests := []struct {
		name       string
		eventType  workflow.EventType
		transition *workflow.Transition
		from       []workflow.Place
		to         []workflow.Place
		workflow   *workflow.Workflow
	}{
		{
			name:       "EventBeforeTransition",
			eventType:  workflow.EventBeforeTransition,
			transition: workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"}),
			from:       []workflow.Place{"start"},
			to:         []workflow.Place{"end"},
			workflow:   nil,
		},
		{
			name:       "EventAfterTransition",
			eventType:  workflow.EventAfterTransition,
			transition: workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"}),
			from:       []workflow.Place{"start"},
			to:         []workflow.Place{"end"},
			workflow:   nil,
		},
		{
			name:       "EventGuard",
			eventType:  workflow.EventGuard,
			transition: workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"}),
			from:       []workflow.Place{"start"},
			to:         []workflow.Place{"end"},
			workflow:   nil,
		},
		{
			name:       "nil transition",
			eventType:  workflow.EventBeforeTransition,
			transition: nil,
			from:       []workflow.Place{"start"},
			to:         []workflow.Place{"end"},
			workflow:   nil,
		},
		{
			name:       "empty from and to",
			eventType:  workflow.EventBeforeTransition,
			transition: workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"}),
			from:       []workflow.Place{},
			to:         []workflow.Place{},
			workflow:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := workflow.NewEvent(context.Background(), tt.eventType, tt.transition, tt.from, tt.to, tt.workflow)

			if event.Type() != tt.eventType {
				t.Errorf("Type() = %v, want %v", event.Type(), tt.eventType)
			}

			if event.Transition() != tt.transition {
				t.Errorf("Transition() = %v, want %v", event.Transition(), tt.transition)
			}

			if len(event.From()) != len(tt.from) {
				t.Errorf("From() length = %d, want %d", len(event.From()), len(tt.from))
			}

			if len(event.To()) != len(tt.to) {
				t.Errorf("To() length = %d, want %d", len(event.To()), len(tt.to))
			}

			if event.Workflow() != tt.workflow {
				t.Errorf("Workflow() = %v, want %v", event.Workflow(), tt.workflow)
			}
		})
	}
}

func TestNewGuardEvent(t *testing.T) {
	tr := workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
	event := workflow.NewGuardEvent(context.Background(), tr, []workflow.Place{"start"}, []workflow.Place{"end"}, nil)

	if event.Type() != workflow.EventGuard {
		t.Errorf("Type() = %v, want %v", event.Type(), workflow.EventGuard)
	}

	if event.IsBlocking() != false {
		t.Errorf("IsBlocking() = %v, want false", event.IsBlocking())
	}

	if event.Transition() != tr {
		t.Errorf("Transition() = %v, want %v", event.Transition(), tr)
	}
}
