// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

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
//	ctx = yaml.WithTemplateValue(ctx, "request", map[string]any{"ip": "192.168.1.1"})
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

// getStringFromContext retrieves a string value stored in the context under a
// string key — set it with WithTemplateValue (Go's context.Value matches on
// exact type, so a caller-defined typed key is invisible here by design).
func getStringFromContext(ctx context.Context, key string) string {
	if val := ctx.Value(key); val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}

	return ""
}

// getValueFromContext retrieves a template value: first from the request
// context under a string key (set via WithTemplateValue), then from the
// workflow's own context. String keys are the one canonical mechanism — a
// caller-defined typed key can never be matched from here (type identity is
// per-package), so no other lookup is attempted.
func getValueFromContext(ctx context.Context, key string, wf *workflow.Workflow) any {
	if val := ctx.Value(key); val != nil {
		return val
	}
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
	var obj any
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
	// For now, support map[string]any which is common in web contexts
	// Users can provide a custom resolver if needed

	return nil
}
