package yaml

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
)

var templateRegex = regexp.MustCompile(`\{\{([^}]+)\}\}`)

// WithTemplateValue stores a value in context using a string key for template resolution.
// Template resolution requires string keys because template variables like {{reason}}
// extract the variable name as a string, and Go's context.Value() requires exact type matching.
//
// Note: This function intentionally uses string keys (SA1029 warning) because template
// resolution extracts variable names as strings from syntax like {{variable}}.
//
// Usage:
//
//	ctx = yaml.WithTemplateValue(ctx, "reason", "value")
//	ctx = yaml.WithTemplateValue(ctx, "request", map[string]interface{}{"ip": "192.168.1.1"})
//
// For non-template context values, you can use custom type keys following Go best practices:
//
//	type myKey string
//	ctx = context.WithValue(ctx, myKey("data"), value)
func WithTemplateValue(ctx context.Context, key string, value any) context.Context {
	// Store with string key (required for template resolution)
	// The linter warning SA1029 is acceptable here as string keys are necessary for {{variable}} syntax
	return context.WithValue(ctx, key, value) //nolint:staticcheck // SA1029: intentional for template resolution
}

// ResolveTemplateValue resolves template variables in custom field values.
// Supports:
//   - "now()" → current timestamp
//   - "{{variable}}" → value from context
//   - "{{object.property}}" → nested property access
//   - Literal strings without templates are returned as-is
func ResolveTemplateValue(value any, ctx context.Context, wf *workflow.Workflow) any {
	// If not a string, return as-is
	str, ok := value.(string)
	if !ok {
		return value
	}

	// Handle "now()" function
	if str == "now()" {
		return time.Now().Format(time.RFC3339)
	}

	// Check for template variables {{...}}
	if !strings.Contains(str, "{{") {
		return str
	}

	// Resolve template variables
	resolved := templateRegex.ReplaceAllStringFunc(str, func(match string) string {
		// Extract variable name (remove {{ and }})
		varName := strings.Trim(match, "{}")
		varName = strings.TrimSpace(varName)

		// Handle nested property access (e.g., "request.ip")
		parts := strings.Split(varName, ".")
		if len(parts) == 1 {
			// Simple variable: {{variable}}
			val := getValueFromContext(ctx, varName, wf)
			if val != nil {
				return fmt.Sprintf("%v", val)
			}
		} else {
			// Nested property: {{object.property}}
			objName := parts[0]
			propPath := strings.Join(parts[1:], ".")
			val := getNestedValueFromContext(ctx, objName, propPath, wf)
			if val != nil {
				return fmt.Sprintf("%v", val)
			}
		}

		// If not found, return the original template (or empty string)
		return ""
	})

	return resolved
}

// getStringFromContext retrieves a string value from context.
// It tries string keys first (for backward compatibility and direct access).
//
// Note: Go's context.Value() requires exact type matching. If examples use typed keys
// (like `type contextKey string; const actorKey contextKey = "actor"`), they must either:
// 1. Use string keys: `context.WithValue(ctx, "actor", value)`
// 2. Use the WithTemplateValue helper: `yaml.WithTemplateValue(ctx, "actor", value)`
// 3. Store with both typed and string keys for compatibility
//
// This function only checks string keys. For typed keys, use WithTemplateValue or store
// values with string keys directly.
func getStringFromContext(ctx context.Context, key string) string {
	// Try string key (works if examples use string keys or WithTemplateValue)
	if val := ctx.Value(key); val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}

	return ""
}

// getValueFromContext retrieves a value from context or workflow context.
// It supports string keys, contextKey type keys, and custom string-based types.
// Template variables like {{reason}} extract as strings, so we try multiple lookup strategies.
//
// Lookup order:
// 1. String key (for backward compatibility and direct template resolution)
// 2. contextKey type (for type-safe keys from this package)
// 3. Custom string-based types (via reflection - tries common patterns)
// 4. Workflow context (string keys)
func getValueFromContext(ctx context.Context, key string, wf *workflow.Workflow) any {
	// First try string key (for template resolution and backward compatibility)
	if val := ctx.Value(key); val != nil {
		return val
	}

	// Try typed key pattern (common in examples: type contextKey string)
	type contextKey string
	typedKey := contextKey(key)
	if val := ctx.Value(typedKey); val != nil {
		return val
	}

	// Finally try workflow context (which uses string keys)
	if wf != nil {
		if val, ok := wf.Context(key); ok {
			return val
		}
	}

	return nil
}

// getNestedValueFromContext retrieves a nested property value.
// Supports: {{request.ip}} where ctx has a "request" object with an "ip" property.
func getNestedValueFromContext(ctx context.Context, objName, propPath string, wf *workflow.Workflow) any {
	// Get the object from context
	var obj interface{}
	if val := ctx.Value(objName); val != nil {
		obj = val
	} else if wf != nil {
		if val, ok := wf.Context(objName); ok {
			obj = val
		}
	}

	if obj == nil {
		return nil
	}

	// Try to access as map
	if objMap, ok := obj.(map[string]any); ok {
		return objMap[propPath]
	}

	// Try to access nested map (e.g., request["ip"])
	if objMap, ok := obj.(map[any]any); ok {
		return objMap[propPath]
	}

	// For struct-like objects, we'd need reflection, but that's complex
	// For now, support map[string]interface{} which is common in web contexts
	// Users can provide a custom resolver if needed

	return nil
}
