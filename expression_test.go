package workflow_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ehabterra/workflow"
)

func TestNewExpressionConstraint(t *testing.T) {
	tests := []struct {
		name          string
		exprStr       string
		wantErr       bool
		errorContains string
	}{
		{
			name:          "empty expression",
			exprStr:       "",
			wantErr:       true,
			errorContains: "expression cannot be empty",
		},
		{
			name:    "valid simple expression",
			exprStr: "true",
			wantErr: false,
		},
		{
			name:    "valid boolean expression",
			exprStr: "1 == 1",
			wantErr: false,
		},
		{
			name:          "invalid syntax",
			exprStr:       "invalid syntax !@#$%",
			wantErr:       true,
			errorContains: "failed to compile expression",
		},
		{
			name:          "unclosed parenthesis",
			exprStr:       "(1 + 2",
			wantErr:       true,
			errorContains: "failed to compile expression",
		},
		{
			name:    "complex valid expression",
			exprStr: "(1 + 2) * 3 > 5",
			wantErr: false,
		},
		{
			name:    "expression with string comparison",
			exprStr: "'test' == 'test'",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constraint, err := workflow.NewExpressionConstraint(tt.exprStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewExpressionConstraint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if tt.errorContains != "" && err != nil && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("NewExpressionConstraint() error = %v, want error containing %q", err, tt.errorContains)
				}
				if constraint != nil {
					t.Error("NewExpressionConstraint() should return nil constraint on error")
				}
			} else {
				if constraint == nil {
					t.Error("NewExpressionConstraint() returned nil constraint, want non-nil")
				}
			}
		})
	}
}

func TestNewExpressionConstraintWithEnv(t *testing.T) {
	tests := []struct {
		name          string
		exprStr       string
		envBuilder    func(workflow.Event) map[string]any
		wantErr       bool
		errorContains string
	}{
		{
			name:          "empty expression",
			exprStr:       "",
			envBuilder:    func(workflow.Event) map[string]any { return make(map[string]any) },
			wantErr:       true,
			errorContains: "expression cannot be empty",
		},
		{
			name:          "nil envBuilder",
			exprStr:       "true",
			envBuilder:    nil,
			wantErr:       true,
			errorContains: "envBuilder cannot be nil",
		},
		{
			name:       "valid expression with custom env",
			exprStr:    "customVar > 10",
			envBuilder: func(workflow.Event) map[string]any { return map[string]any{"customVar": 20} },
			wantErr:    false,
		},
		{
			name:          "invalid syntax with custom env",
			exprStr:       "invalid !@#",
			envBuilder:    func(workflow.Event) map[string]any { return make(map[string]any) },
			wantErr:       true,
			errorContains: "failed to compile expression",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constraint, err := workflow.NewExpressionConstraintWithEnv(tt.exprStr, tt.envBuilder)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewExpressionConstraintWithEnv() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				if tt.errorContains != "" && err != nil && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("NewExpressionConstraintWithEnv() error = %v, want error containing %q", err, tt.errorContains)
				}
				if constraint != nil {
					t.Error("NewExpressionConstraintWithEnv() should return nil constraint on error")
				}
			} else {
				if constraint == nil {
					t.Error("NewExpressionConstraintWithEnv() returned nil constraint, want non-nil")
				}
			}
		})
	}
}

func TestExpressionConstraint_Validate(t *testing.T) {
	// Helper to create a test workflow
	createWorkflow := func(contextData map[string]any) *workflow.Workflow {
		def, err := workflow.NewDefinition(
			[]workflow.Place{"start", "end"},
			[]workflow.Transition{*workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})},
		)
		if err != nil {
			t.Fatalf("failed to create definition: %v", err)
		}
		wf, err := workflow.NewWorkflow("test", def, "start")
		if err != nil {
			t.Fatalf("failed to create workflow: %v", err)
		}
		for k, v := range contextData {
			wf.SetContext(k, v)
		}
		return wf
	}

	// Helper to create a test event
	createEvent := func(wf *workflow.Workflow, transition *workflow.Transition) workflow.Event {
		from := []workflow.Place{}
		to := []workflow.Place{}
		if transition != nil {
			from = transition.From()
			to = transition.To()
		}
		return workflow.NewGuardEvent(context.Background(), transition, from, to, wf)
	}

	tests := []struct {
		name            string
		exprStr         string
		setupWorkflow   func() *workflow.Workflow
		setupTransition func() *workflow.Transition
		wantErr         bool
		errorContains   string
		shouldBlock     bool
	}{
		{
			name:    "simple true expression",
			exprStr: "true",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "simple false expression blocks",
			exprStr: "false",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:       true,
			shouldBlock:   true,
			errorContains: "transition not allowed",
		},
		{
			name:    "context value access - exists",
			exprStr: "workflow.Context('amount') > 100",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"amount": 200})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "context value access - does not exist",
			exprStr: "getContext('amount', 0) > 100",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true, // Returns 0, which is not > 100
		},
		{
			name:    "hasRole with []string - found",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": []string{"admin", "user"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "hasRole with []string - not found",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": []string{"user", "guest"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "hasRole with []any - found",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": []any{"admin", "user"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "hasRole with string - matches",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": "admin"})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "hasRole with string - no match",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": "user"})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "hasRole - roles not set",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "hasRole - roles is nil",
			exprStr: "hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				wf := createWorkflow(nil)
				wf.SetContext("roles", nil)
				return wf
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "hasPermission with []string - found",
			exprStr: "hasPermission('write')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"permissions": []string{"read", "write"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "hasPermission with []string - not found",
			exprStr: "hasPermission('write')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"permissions": []string{"read"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "hasPermission with []any - found",
			exprStr: "hasPermission('write')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"permissions": []any{"read", "write"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "hasPermission with string - matches",
			exprStr: "hasPermission('write')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"permissions": "write"})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "hasPermission - permissions not set",
			exprStr: "hasPermission('write')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "in operator - value found",
			exprStr: "'admin' in ['admin', 'user']",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "in operator - value not found",
			exprStr: "'admin' in ['user', 'guest']",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "inPlace - place exists",
			exprStr: "inPlace('start')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "inPlace - place does not exist",
			exprStr: "inPlace('end')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "getContext - key exists",
			exprStr: "getContext('amount', 0) > 100",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"amount": 200})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "getContext - key does not exist, uses default",
			exprStr: "getContext('amount', 0) == 0",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "getContext - key is nil, uses default",
			exprStr: "getContext('amount', 0) == 0",
			setupWorkflow: func() *workflow.Workflow {
				wf := createWorkflow(nil)
				wf.SetContext("amount", nil)
				return wf
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "complex expression with multiple conditions",
			exprStr: "workflow.Context('amount') > 100 and hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{
					"amount": 200,
					"roles":  []string{"admin"},
				})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "complex expression - first condition fails",
			exprStr: "workflow.Context('amount') > 100 and hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{
					"amount": 50,
					"roles":  []string{"admin"},
				})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "complex expression - second condition fails",
			exprStr: "workflow.Context('amount') > 100 and hasRole('admin')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{
					"amount": 200,
					"roles":  []string{"user"},
				})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "or condition - first true",
			exprStr: "hasRole('admin') or hasRole('user')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": []string{"admin"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "or condition - second true",
			exprStr: "hasRole('admin') or hasRole('user')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": []string{"user"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "or condition - both false",
			exprStr: "hasRole('admin') or hasRole('user')",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"roles": []string{"guest"}})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "transition name access",
			exprStr: "transition == 'test'",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "from places access",
			exprStr: "len(from) > 0",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "to places access",
			exprStr: "len(to) > 0",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "nil workflow - inPlace returns false",
			exprStr: "inPlace('start')",
			setupWorkflow: func() *workflow.Workflow {
				return nil
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:     true,
			shouldBlock: true,
		},
		{
			name:    "nil workflow - getContext returns default",
			exprStr: "getContext('amount', 0) == 0",
			setupWorkflow: func() *workflow.Workflow {
				return nil
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "nil transition",
			exprStr: "true",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return nil
			},
			wantErr: false,
		},
		{
			name:    "expression returns non-boolean - integer",
			exprStr: "42",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:       true,
			errorContains: "expression must return boolean",
		},
		{
			name:    "expression returns non-boolean - string",
			exprStr: "'hello'",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr:       true,
			errorContains: "expression must return boolean",
		},
		{
			name:    "expression with transition metadata",
			exprStr: "transition_priority > 5",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(nil)
			},
			setupTransition: func() *workflow.Transition {
				tr := workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
				tr.SetMetadata("metadata", map[string]any{"priority": 10})
				return tr
			},
			wantErr: false,
		},
		{
			name:    "expression with numeric context values",
			exprStr: "workflow.Context('count') + workflow.Context('offset') > 10",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{
					"count":  7,
					"offset": 5,
				})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "expression with string context values",
			exprStr: "workflow.Context('status') == 'active'",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"status": "active"})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
		{
			name:    "expression with boolean context values",
			exprStr: "workflow.Context('enabled') == true",
			setupWorkflow: func() *workflow.Workflow {
				return createWorkflow(map[string]any{"enabled": true})
			},
			setupTransition: func() *workflow.Transition {
				return workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constraint, err := workflow.NewExpressionConstraint(tt.exprStr)
			if err != nil {
				t.Fatalf("NewExpressionConstraint() failed: %v", err)
			}

			wf := tt.setupWorkflow()
			tr := tt.setupTransition()
			event := createEvent(wf, tr)

			err = constraint.Validate(event)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if tt.errorContains != "" && err != nil && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Validate() error = %v, want error containing %q", err, tt.errorContains)
				}
				if tt.shouldBlock && err != nil && !errors.Is(err, workflow.ErrTransitionNotAllowed) {
					t.Errorf("Validate() error = %v, want ErrTransitionNotAllowed", err)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestExpressionConstraint_Validate_CustomEnv(t *testing.T) {
	// Test with custom environment builder
	customEnvBuilder := func(event workflow.Event) map[string]any {
		env := make(map[string]any)
		env["customVar"] = 42
		env["customFunc"] = func(x int) int { return x * 2 }
		return env
	}

	constraint, err := workflow.NewExpressionConstraintWithEnv("customVar > 40 and customFunc(10) == 20", customEnvBuilder)
	if err != nil {
		t.Fatalf("NewExpressionConstraintWithEnv() failed: %v", err)
	}

	def, err := workflow.NewDefinition(
		[]workflow.Place{"start", "end"},
		[]workflow.Transition{*workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})},
	)
	if err != nil {
		t.Fatalf("failed to create definition: %v", err)
	}

	wf, err := workflow.NewWorkflow("test", def, "start")
	if err != nil {
		t.Fatalf("failed to create workflow: %v", err)
	}

	tr := workflow.MustNewTransition("test", []workflow.Place{"start"}, []workflow.Place{"end"})
	event := workflow.NewGuardEvent(context.Background(), tr, tr.From(), tr.To(), wf)

	err = constraint.Validate(event)
	if err != nil {
		t.Errorf("Validate() with custom env error = %v, want nil", err)
	}
}
