package workflow

import (
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

// Validate evaluates the expression and returns error if transition should be blocked.
func (c *ExpressionConstraint) Validate(event Event) error {
	// Build evaluation environment from event
	env := c.envBuilder(event)

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
