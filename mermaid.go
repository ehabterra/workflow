package workflow

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// escapeMermaidLabel escapes special characters in Mermaid labels using HTML entities
func escapeMermaidLabel(label string) string {
	// Replace special characters that can cause Mermaid parsing issues
	// Use HTML entity numbers without & prefix (e.g., #58; for colon)
	label = strings.ReplaceAll(label, ":", "#58;")
	// Replace single quotes with corresponding html entity
	label = strings.ReplaceAll(label, "'", "#39;")

	return label
}

// simplifyGuard simplifies guard expressions for better readability
// It preserves the logical structure (and/or, parentheses) but simplifies function calls
func simplifyGuard(guard string) string {
	if guard == "" {
		return ""
	}

	// Start with the original guard
	simplified := guard

	// Replace workflow.Context('key') with just 'key'
	// Handle both single and double quotes
	contextPatternSingle := regexp.MustCompile(`workflow\.Context\('([^']+)'\)`)
	contextPatternDouble := regexp.MustCompile(`workflow\.Context\("([^"]+)"\)`)
	simplified = contextPatternSingle.ReplaceAllString(simplified, `$1`)
	simplified = contextPatternDouble.ReplaceAllString(simplified, `$1`)

	// Replace hasRole('name') with role: name
	rolePatternSingle := regexp.MustCompile(`hasRole\('([^']+)'\)`)
	rolePatternDouble := regexp.MustCompile(`hasRole\("([^"]+)"\)`)
	simplified = rolePatternSingle.ReplaceAllString(simplified, `role: $1`)
	simplified = rolePatternDouble.ReplaceAllString(simplified, `role: $1`)

	// Replace getContext('name', default) with name
	getContextPatternSingle := regexp.MustCompile(`getContext\('([^']+)',[^)]+\)`)
	getContextPatternDouble := regexp.MustCompile(`getContext\("([^"]+)",[^)]+\)`)
	simplified = getContextPatternSingle.ReplaceAllString(simplified, `$1`)
	simplified = getContextPatternDouble.ReplaceAllString(simplified, `$1`)

	// Replace hasPermission('name') with permission: name
	permissionPatternSingle := regexp.MustCompile(`hasPermission\('([^']+)'\)`)
	permissionPatternDouble := regexp.MustCompile(`hasPermission\("([^"]+)"\)`)
	simplified = permissionPatternSingle.ReplaceAllString(simplified, `permission: $1`)
	simplified = permissionPatternDouble.ReplaceAllString(simplified, `permission: $1`)

	// Replace inPlace('name') with place name
	inPlacePatternSingle := regexp.MustCompile(`inPlace\('([^']+)'\)`)
	inPlacePatternDouble := regexp.MustCompile(`inPlace\("([^"]+)"\)`)
	simplified = inPlacePatternSingle.ReplaceAllString(simplified, `place: $1`)
	simplified = inPlacePatternDouble.ReplaceAllString(simplified, `place: $1`)

	return simplified
}

// Diagram generates a Mermaid state diagram for the workflow
func (w *Workflow) Diagram() string {
	var diagram strings.Builder
	diagram.WriteString("stateDiagram-v2\n")
	diagram.WriteString("    direction TB\n") // Top to bottom direction for better layout
	diagram.WriteString("    classDef currentPlace font-weight:bold,stroke-width:4px\n")

	// Add all places
	for _, place := range w.definition.Places {
		fmt.Fprintf(&diagram, "    %s\n", place)
	}

	// Add all transitions
	for _, trans := range w.definition.Transitions {
		// Get guard from metadata
		guard := ""
		if guardVal, ok := trans.Metadata("guard"); ok {
			if guardStr, ok := guardVal.(string); ok {
				guard = guardStr
			}
		}

		// Use transition name as the label only - guard will be shown in tooltip
		escapedName := escapeMermaidLabel(trans.Name())
		displayLabel := escapedName

		// Add data attributes for tooltip if guard exists
		if guard != "" {
			simplified := simplifyGuard(guard)
			if simplified != "" {
				// Escape HTML entities for attribute values
				escapedNameAttr := escapeMermaidLabel(trans.Name())
				escapedGuardAttr := escapeMermaidLabel(guard) // Use full guard for tooltip
				escapedSimplifiedAttr := escapeMermaidLabel(simplified)
				// Add data attributes to the label for tooltip
				displayLabel = fmt.Sprintf(`<span class="transition-label" data-transition-name="%s" data-guard="%s" data-guard-simplified="%s">%s</span>`,
					escapedNameAttr, escapedGuardAttr, escapedSimplifiedAttr, escapedName)
			}
		} else {
			// Even without guard, add data attribute for transition name
			escapedNameAttr := html.EscapeString(trans.Name())
			displayLabel = fmt.Sprintf(`<span class="transition-label" data-transition-name="%s">%s</span>`, escapedNameAttr, escapedName)
		}

		// Handle multiple to places
		if len(trans.To()) > 1 {
			// This is a fork
			forkState := fmt.Sprintf("%s_fork", trans.Name())
			fmt.Fprintf(&diagram, "    state %s <<fork>>\n", forkState)
			if len(trans.From()) > 1 {
				// This is a join
				joinState := fmt.Sprintf("%s_join", trans.Name())
				fmt.Fprintf(&diagram, "    state %s <<join>>\n", joinState)
				for _, from := range trans.From() {
					fmt.Fprintf(&diagram, "    %s --> %s : %s\n", from, joinState, displayLabel)
				}
				fmt.Fprintf(&diagram, "    %s --> %s\n", joinState, forkState)
			} else {
				fmt.Fprintf(&diagram, "    %s --> %s : %s\n", trans.From()[0], forkState, displayLabel)
			}
			for _, to := range trans.To() {
				fmt.Fprintf(&diagram, "    %s --> %s\n", forkState, to)
			}
		} else {
			if len(trans.From()) > 1 {
				// This is a join
				joinState := fmt.Sprintf("%s_join", trans.Name())
				fmt.Fprintf(&diagram, "    state %s <<join>>\n", joinState)
				for _, from := range trans.From() {
					fmt.Fprintf(&diagram, "    %s --> %s : %s\n", from, joinState, displayLabel)
				}
				fmt.Fprintf(&diagram, "    %s --> %s\n", joinState, trans.To()[0])
			} else {
				// Regular transition
				fmt.Fprintf(&diagram, "    %s --> %s : %s\n", trans.From()[0], trans.To()[0], displayLabel)
			}
		}
	}

	// Add current place highlighting
	currentPlaces := w.marking.Places()
	if len(currentPlaces) > 0 {
		diagram.WriteString("\n    %% Current places\n")
		for _, place := range currentPlaces {
			fmt.Fprintf(&diagram, "    class %s currentPlace\n", place)
		}
	}

	diagram.WriteString("\n    %% Initial place\n")
	fmt.Fprintf(&diagram, "    [*] --> %s\n", w.InitialPlace())

	return diagram.String()
}
