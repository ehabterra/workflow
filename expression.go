// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// ExpressionConstraint evaluates an expression string to determine if transition is allowed.
// Uses expr-lang/expr for safe expression evaluation.
type ExpressionConstraint struct {
	expression string
	program    *vm.Program
	envBuilder func(Event) map[string]any

	// txEnvBuilder, when set, makes this a TRANSACTION-SCOPED constraint: it is
	// handed the transaction the firing runs inside, so the functions it puts in
	// the environment can query host state as of that transaction. It is
	// mutually exclusive with envBuilder.
	txEnvBuilder TxEnvBuilder
}

// TxEnvBuilder contributes the transaction-backed part of a guard environment.
// tx is the backend's own handle — *sql.Tx for the shipped SQL backends — and is
// never nil when the builder is called.
//
// What it returns is ADDED to the standard guard environment (workflow context,
// `token`/`tokens`, hasRole and friends), overriding same-named entries. So a
// guard can compare something read live against something the host passed in —
// `actor != submitterOf()` — which is what most of them do.
//
// The point is to close the gap between deciding and committing. Without it a
// host resolves its decision inputs, THEN fires, and anything that changed in
// between is silently stale; the values it injected into the workflow context
// are a snapshot of a moment that has passed. A builder given the transaction
// can instead expose functions that answer as of the transaction that is about
// to commit:
//
//	wfyaml.NewLoaderWithTxEnv(func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
//	    q := sqlc.New(tx.(*sql.Tx))
//	    return map[string]any{
//	        "readyGate": func() bool { return q.EveryLineHasACostCode(ctx, ev.Workflow().Name()) },
//	    }
//	})
//
// RETRIES: Manager.Execute retries the whole load-fire-save cycle on
// ErrConflict, and each attempt opens a NEW transaction, so the builder runs
// again and the guard re-decides against the state the winning attempt will
// actually commit against. That is the intended behavior — never cache the
// answer across attempts.
type TxEnvBuilder func(ctx context.Context, tx any, ev Event) map[string]any

// TxScopedConstraint is a Constraint that must be evaluated inside the firing
// transaction. Manager.Execute detects a definition containing one and runs the
// whole load → fire → save cycle inside a single transaction (which requires a
// TxScopedStorage backend), rather than firing in memory and opening the
// transaction only at the save.
//
// Implement it on a custom Go constraint to opt into the same treatment;
// NewTxExpressionConstraint is the expression-language version.
type TxScopedConstraint interface {
	Constraint

	// NeedsTx reports that this constraint cannot be evaluated without the
	// firing transaction.
	NeedsTx() bool
}

// NewExpressionConstraint creates a new expression constraint.
// The expression should return a boolean value.
// If the expression returns false or evaluates to an error, the transition is blocked.
func NewExpressionConstraint(exprStr string) (*ExpressionConstraint, error) {
	if exprStr == "" {
		return nil, fmt.Errorf("expression cannot be empty")
	}

	// Compile the expression with a basic environment to validate syntax
	program, err := expr.Compile(exprStr)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression '%s': %w", exprStr, err)
	}

	return &ExpressionConstraint{
		expression: exprStr,
		program:    program,
		envBuilder: defaultEnvBuilder,
	}, nil
}

// NewExpressionConstraintWithEnv creates a new expression constraint with a custom environment builder.
// This allows you to provide custom functions and variables for expression evaluation.
func NewExpressionConstraintWithEnv(exprStr string, envBuilder func(Event) map[string]any) (*ExpressionConstraint, error) {
	if exprStr == "" {
		return nil, fmt.Errorf("expression cannot be empty")
	}

	if envBuilder == nil {
		return nil, fmt.Errorf("envBuilder cannot be nil")
	}

	// Compile the expression with a basic environment to validate syntax
	program, err := expr.Compile(exprStr)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression '%s': %w", exprStr, err)
	}

	return &ExpressionConstraint{
		expression: exprStr,
		program:    program,
		envBuilder: envBuilder,
	}, nil
}

// NewTxExpressionConstraint creates a TRANSACTION-SCOPED expression constraint:
// the environment is built by a TxEnvBuilder that receives the transaction the
// firing runs inside, so the guard can consult host state as of that
// transaction instead of a value read before it opened.
//
// A definition containing one requires Manager.Execute against a
// TxScopedStorage backend. Evaluating it outside a firing transaction — a bare
// Workflow.ApplyTransition, a CanTransition probe, a non-transactional backend —
// fails with ErrNoTransaction rather than quietly answering from stale state.
// Keep ordinary preconditions in a plain guard, which works everywhere; reach
// for this one only where staleness is a correctness problem.
func NewTxExpressionConstraint(exprStr string, builder TxEnvBuilder) (*ExpressionConstraint, error) {
	if exprStr == "" {
		return nil, fmt.Errorf("expression cannot be empty")
	}
	if builder == nil {
		return nil, fmt.Errorf("TxEnvBuilder cannot be nil")
	}

	program, err := expr.Compile(exprStr)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression '%s': %w", exprStr, err)
	}

	return &ExpressionConstraint{
		expression:   exprStr,
		program:      program,
		txEnvBuilder: builder,
	}, nil
}

// NeedsTx reports whether this constraint is transaction-scoped (built with
// NewTxExpressionConstraint). It implements TxScopedConstraint.
func (c *ExpressionConstraint) NeedsTx() bool { return c.txEnvBuilder != nil }

// Validate evaluates the expression and returns error if transition should be blocked.
func (c *ExpressionConstraint) Validate(event Event) error {
	// Build evaluation environment from event
	env, err := c.buildEnv(event)
	if err != nil {
		return err
	}

	// Evaluate expression
	result, err := expr.Run(c.program, env)
	if err != nil {
		return fmt.Errorf("expression evaluation error: %w", err)
	}

	// Expression should return boolean
	allowed, ok := result.(bool)
	if !ok {
		return fmt.Errorf("expression must return boolean, got %T", result)
	}

	if !allowed {
		return ErrGuardRejected
	}

	return nil
}

// buildEnv assembles the evaluation environment. For a transaction-scoped
// constraint it demands the firing transaction: a guard whose whole purpose is
// to be current must never fall back to evaluating without one.
func (c *ExpressionConstraint) buildEnv(event Event) (map[string]any, error) {
	if c.txEnvBuilder == nil {
		return c.envBuilder(event), nil
	}
	wf := event.Workflow()
	if wf == nil {
		return nil, fmt.Errorf("%w: transaction-scoped guard %q has no workflow", ErrNoTransaction, c.expression)
	}
	tx := wf.tx()
	if tx == nil {
		return nil, fmt.Errorf("%w: guard %q must be evaluated inside a firing transaction — "+
			"drive it with Manager.Execute against a TxScopedStorage backend", ErrNoTransaction, c.expression)
	}
	// The builder's entries are ADDED to the standard environment rather than
	// replacing it, because a transaction-scoped guard is almost always a mix:
	// something read live (submitterOf()) compared against something the host
	// passed in (actor). Making the builder responsible for re-supplying the
	// context would turn every such guard into a silent nil comparison.
	env := defaultEnvBuilder(event)
	maps.Copy(env, c.txEnvBuilder(event.Context(), tx, event))
	return env, nil
}

// defaultEnvBuilder creates the default evaluation environment from the event.
//
// The workflow context is copied in first, then a set of reserved names is added
// for use in guard expressions: workflow, transition, transition_<key>, from, to,
// token, tokens, and the helpers hasRole, hasPermission, in. These reserved names
// shadow any workflow-context key of the same name, so avoid using them as context
// keys.
func defaultEnvBuilder(event Event) map[string]any {
	env := make(map[string]any)

	// Add workflow context
	wf := event.Workflow()
	if wf != nil {
		// Add all context values
		wf.mu.RLock()
		maps.Copy(env, wf.context)
		wf.mu.RUnlock()

		// Add workflow reference
		env["workflow"] = wf
	}

	// Add transition information
	transition := event.Transition()
	if transition != nil {
		env["transition"] = transition.Name()
		// Add transition metadata
		if metadata, ok := transition.Metadata("metadata"); ok {
			if metaMap, ok := metadata.(map[string]any); ok {
				for k, v := range metaMap {
					env["transition_"+k] = v
				}
			}
		}
	}

	// Add place information
	env["from"] = event.From()
	env["to"] = event.To()

	// Add token data so guards can route on it. For per-token firing there is a
	// single token, exposed as `token` (e.g. token.amount > 1000); all involved
	// tokens' data is available as `tokens`.
	if toks := event.Tokens(); len(toks) > 0 {
		datas := make([]TokenData, len(toks))
		for i, t := range toks {
			datas[i] = t.Data()
		}
		env["tokens"] = datas
		if len(toks) == 1 {
			env["token"] = datas[0]
		}
	}

	// Add helper functions
	env["hasRole"] = func(role string) bool {
		// First try to get roles from environment (which was copied from context)
		var roles any
		var ok bool

		// Check environment first (roles are copied there at line 103)
		if rolesVal, exists := env["roles"]; exists {
			roles = rolesVal
			ok = true
		} else if wf != nil {
			// Fall back to workflow context if not in environment
			roles, ok = wf.Context("roles")
		}

		if !ok || roles == nil {
			return false
		}

		// Handle different role types
		switch v := roles.(type) {
		case []string:
			return slices.Contains(v, role)
		case []any:
			for _, r := range v {
				if r == role {
					return true
				}
			}
		case string:
			return v == role
		}
		return false
	}

	env["hasPermission"] = func(permission string) bool {
		permissions, ok := wf.Context("permissions")
		if !ok {
			return false
		}
		switch v := permissions.(type) {
		case []string:
			return slices.Contains(v, permission)
		case []any:
			for _, p := range v {
				if p == permission {
					return true
				}
			}
		case string:
			return v == permission
		}
		return false
	}

	env["in"] = func(value any, list []any) bool {
		return slices.Contains(list, value)
	}

	// Helper function to check if workflow is currently in a specific place
	env["inPlace"] = func(placeName string) bool {
		if wf == nil {
			return false
		}
		return wf.Marking().HasPlace(Place(placeName))
	}

	// Helper function to safely get context value with default
	// Usage: getContext('key', defaultValue) or getContext('key', 0)
	env["getContext"] = func(key string, defaultValue any) any {
		if wf == nil {
			return defaultValue
		}
		value, ok := wf.Context(key)
		if !ok || value == nil {
			return defaultValue
		}
		return value
	}

	return env
}
