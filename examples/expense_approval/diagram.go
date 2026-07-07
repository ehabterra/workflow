package main

import (
	"fmt"
	"strings"

	"github.com/ehabterra/workflow"
)

// This file renders the nets as Mermaid FLOWCHARTS purpose-built for the UI —
// richer than the library's generic Workflow.Diagram() (stateDiagram): every
// transition is a typed node (person / timer / automatic), guards are visible
// on the routing edges, reset arcs are dotted "cancels" edges, OR-inputs are
// labeled "either", the live marking is highlighted, and colored-token places
// carry live token/amount badges. The palette matches the app's CSS.

// transitionKind classifies who fires a transition — app knowledge the
// definition cannot carry (the engine never fires anything by itself).
// Timers are derived from TimeoutAfter; everything else is listed here.
var transitionKind = map[string]string{
	"submit":          "person", // submitter
	"submit_auto":     "auto",   // guard-routed fast path
	"legal_approve":   "person",
	"legal_reject":    "person",
	"finance_approve": "person",
	"finance_reject":  "person",
	"revise":          "person",
	"finalize":        "auto",   // host fires it after the second approval
	"mark_paid":       "auto",   // batch run advances the expense
	"pay":             "auto",   // batch run, per token
	"release":         "person", // payment reviewer
}

// placeBadge is extra live text rendered under a place name (token counts,
// amounts). Empty = no badge.
type placeBadge func(p workflow.Place) string

// mermaidNet renders one definition as a flowchart. current holds the marked
// places of a live instance (nil for a structure-only diagram); badge adds
// live annotations under place names.
func mermaidNet(def *workflow.Definition, current map[workflow.Place]bool, badge placeBadge) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")

	// Places: stadium nodes, highlighted when the live marking holds them.
	for _, p := range def.AllPlaces() {
		label := string(p)
		if badge != nil {
			if extra := badge(p); extra != "" {
				label += "<br/><b>" + extra + "</b>"
			}
		}
		fmt.Fprintf(&b, "    p_%s([\"%s\"])\n", nodeID(string(p)), label)
		switch {
		case current[p]:
			fmt.Fprintf(&b, "    class p_%s current\n", nodeID(string(p)))
		case p == "rejected":
			fmt.Fprintf(&b, "    class p_%s bad\n", nodeID(string(p)))
		case p == "paid" || p == "paid_out":
			fmt.Fprintf(&b, "    class p_%s settled\n", nodeID(string(p)))
		default:
			fmt.Fprintf(&b, "    class p_%s place\n", nodeID(string(p)))
		}
	}

	// Transitions: one typed node each, with guard shown on its routing edge.
	var timerEdges, resetEdges []int
	edge := 0
	for _, t := range def.AllTransitions() {
		id := "t_" + nodeID(t.Name())
		kind := transitionKind[t.Name()]
		if _, timed := t.TimeoutAfter(); timed {
			kind = "timer"
		}
		if kind == "" {
			kind = "person"
		}
		label := displayName(t.Name())
		if d, ok := t.TimeoutAfter(); ok {
			label = "⏱ " + label + "<br/>after " + d.String()
		}
		fmt.Fprintf(&b, "    %s[\"%s\"]\n    class %s %s\n", id, label, id, kind)

		inLabel := ""
		if t.FromAny() {
			inLabel = "|either|"
		}
		for _, from := range t.From() {
			fmt.Fprintf(&b, "    p_%s -->%s %s\n", nodeID(string(from)), inLabel, id)
			if kind == "timer" {
				timerEdges = append(timerEdges, edge)
			}
			edge++
		}
		outLabel := ""
		if g, ok := t.Metadata("guard"); ok {
			if gs, ok := g.(string); ok && gs != "" {
				outLabel = "|\"❰ " + escapeGuard(gs) + " ❱\"|"
			}
		}
		for _, to := range t.To() {
			fmt.Fprintf(&b, "    %s -->%s p_%s\n", id, outLabel, nodeID(string(to)))
			if kind == "timer" {
				timerEdges = append(timerEdges, edge)
			}
			edge++
		}
		for _, reset := range t.Resets() {
			fmt.Fprintf(&b, "    %s -. cancels .-> p_%s\n", id, nodeID(string(reset)))
			resetEdges = append(resetEdges, edge)
			edge++
		}
	}

	// Palette (mirrors the app CSS): places quiet, people pine, timers amber,
	// automation sky, cancellation rust.
	b.WriteString(`    classDef place fill:#FFFFFF,stroke:#5C6660,stroke-width:1px,color:#1C2321
    classDef current fill:#E3EFE9,stroke:#1B5E4A,stroke-width:3px,color:#14483A,font-weight:bold
    classDef bad fill:#F9E8E6,stroke:#A03028,color:#A03028
    classDef settled fill:#E2EDF5,stroke:#23577E,color:#23577E
    classDef person fill:#1B5E4A,stroke:#14483A,color:#FFFFFF
    classDef timer fill:#FBF0DE,stroke:#B45309,color:#B45309
    classDef auto fill:#E2EDF5,stroke:#23577E,color:#23577E
`)
	for _, i := range timerEdges {
		fmt.Fprintf(&b, "    linkStyle %d stroke:#B45309,stroke-dasharray:6 3\n", i)
	}
	for _, i := range resetEdges {
		fmt.Fprintf(&b, "    linkStyle %d stroke:#A03028\n", i)
	}
	return b.String()
}

// displayName renders a transition name for humans ("legal_approve" ->
// "legal approve").
func displayName(name string) string {
	return strings.ReplaceAll(name, "_", " ")
}

// nodeID sanitizes a name into a Mermaid-safe identifier.
func nodeID(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, s)
}

// escapeGuard keeps a guard expression readable inside a quoted Mermaid edge
// label.
func escapeGuard(g string) string {
	g = strings.ReplaceAll(g, "<=", "≤")
	g = strings.ReplaceAll(g, ">=", "≥")
	g = strings.ReplaceAll(g, "<", "#60;")
	g = strings.ReplaceAll(g, ">", "#62;")
	g = strings.ReplaceAll(g, "\"", "#quot;")
	return g
}
