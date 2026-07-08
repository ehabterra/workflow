package workflow

import (
	"fmt"
	"regexp"
	"strings"
)

// This file renders definitions and live instances as Mermaid flowcharts
// designed to be read by technical AND non-technical people:
//
//   - Places are stadium (rounded) nodes — states where work rests. End
//     states (places nothing consumes from) get a distinct terminal style.
//   - Transitions are rectangles, color-typed: timed transitions (⏱, amber,
//     dashed edges) are derived from TimeoutAfter; a transition can choose
//     its visual class with the "diagram_class" metadata key ("person",
//     "auto", or any custom classDef the host page defines) — useful because
//     only the host knows WHO fires a transition.
//   - Guard expressions appear visibly on the routing edges: ❰ amount ≤ 100 ❱.
//   - Reset arcs (cancellation regions) are dotted red "cancels" edges.
//   - Fork/join gateway diamonds (the BPMN gateway idiom) make split/merge
//     semantics explicit AND visible: a multi-input transition joins through
//     a diamond that SAYS its semantics — ◇all (AND-join, every input
//     required) or ◇any (OR-input/FromAny, exactly one consumed) — and a
//     multi-output transition forks through a ◇all diamond (outputs are
//     always "produce all"). XOR-splits need no gateway: they are
//     alternative guarded transitions out of one place, so the place is the
//     choice point and the guards label the routes.
//   - On a live instance (Workflow.Diagram), the current marking is
//     highlighted and places holding colored tokens carry a ⬤×N badge.
//
// The classDef palette is neutral and self-contained; a host page may
// restyle it or add classes and reference them via diagram_class.

// diagramClassMeta is the metadata key a transition may set to choose its
// visual class ("person", "auto", or a host-defined classDef name).
const diagramClassMeta = "diagram_class"

// diagramGroupMeta is the metadata key a place may set to name the region it
// belongs to; same-group places are boxed together in a Mermaid subgraph so
// parallel lanes read as distinct regions.
const diagramGroupMeta = "diagram_group"

// escapeMermaidLabel escapes characters that would break or restyle Mermaid
// labels, using HTML entity numbers without the & prefix (Mermaid's own
// convention, e.g. #58; for a colon).
func escapeMermaidLabel(label string) string {
	label = strings.ReplaceAll(label, ":", "#58;")
	label = strings.ReplaceAll(label, "'", "#39;")
	label = strings.ReplaceAll(label, "\"", "#34;")
	// Angle brackets would otherwise read as HTML — guard expressions are
	// full of them ("amount <= 100").
	label = strings.ReplaceAll(label, "<", "#60;")
	label = strings.ReplaceAll(label, ">", "#62;")
	return label
}

// prettifyGuard renders a guard expression for humans: comparison operators
// become their mathematical forms and function-call noise is simplified (see
// simplifyGuard), keeping the logical structure intact.
func prettifyGuard(guard string) string {
	g := simplifyGuard(guard)
	g = strings.ReplaceAll(g, "<=", "≤")
	g = strings.ReplaceAll(g, ">=", "≥")
	g = strings.ReplaceAll(g, "==", "=")
	return escapeMermaidLabel(g)
}

// simplifyGuard simplifies guard expressions for better readability.
// It preserves the logical structure (and/or, parentheses) but simplifies
// function calls: hasRole('x') -> role x, hasPermission('y') -> can y,
// workflow.Context('k') -> k.
func simplifyGuard(guard string) string {
	if guard == "" {
		return ""
	}
	simplified := guard
	simplified = regexp.MustCompile(`workflow\.Context\('([^']+)'\)`).ReplaceAllString(simplified, "$1")
	simplified = regexp.MustCompile(`workflow\.Context\("([^"]+)"\)`).ReplaceAllString(simplified, "$1")
	simplified = regexp.MustCompile(`hasRole\('([^']+)'\)`).ReplaceAllString(simplified, "role $1")
	simplified = regexp.MustCompile(`hasRole\("([^"]+)"\)`).ReplaceAllString(simplified, "role $1")
	simplified = regexp.MustCompile(`hasPermission\('([^']+)'\)`).ReplaceAllString(simplified, "can $1")
	simplified = regexp.MustCompile(`hasPermission\("([^"]+)"\)`).ReplaceAllString(simplified, "can $1")
	simplified = regexp.MustCompile(`\s+`).ReplaceAllString(simplified, " ")
	return strings.TrimSpace(simplified)
}

// nodeID sanitizes a place or transition name into a Mermaid-safe identifier.
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

// humanizeName renders a place or transition name for display:
// "legal_approve" -> "legal approve".
func humanizeName(name string) string {
	return escapeMermaidLabel(strings.ReplaceAll(name, "_", " "))
}

// Diagram renders the definition's structure as a Mermaid flowchart (no
// instance state — a Definition does not know its initial marking; use
// Workflow.Diagram for the entry marker, current marking, and token badges).
// See the notes at the top of mermaid.go for the visual language.
func (d *Definition) Diagram() string {
	return renderDiagram(d, nil, nil, nil)
}

// Diagram renders this instance's net as a Mermaid flowchart with the
// current marking highlighted; places holding colored tokens carry a ⬤×N
// count badge.
func (w *Workflow) Diagram() string {
	w.mu.RLock()
	current := make(map[Place]bool)
	tokenCounts := make(map[Place]int)
	for _, p := range w.marking.Places() {
		current[p] = true
		n := 0
		for _, tok := range w.marking.TokensAt(p) {
			if isColored(tok) {
				n++
			}
		}
		tokenCounts[p] = n
	}
	def := w.definition
	initial := w.initialPlaces
	w.mu.RUnlock()
	return renderDiagram(def, initial, current, tokenCounts)
}

// renderDiagram is the shared flowchart generator. initial marks the entry
// place(s) (classic ●-marker edges); current marks the live places (nil for
// structure-only); tokenCounts carries per-place colored token counts for
// the ⬤×N badges.
func renderDiagram(def *Definition, initial []Place, current map[Place]bool, tokenCounts map[Place]int) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	if def == nil {
		return b.String()
	}

	// The classic Petri-net/statechart entry marker: a filled dot feeding
	// each initial place.
	if len(initial) > 0 {
		b.WriteString("    START((( )))\n    class START startMarker\n")
		for _, p := range initial {
			fmt.Fprintf(&b, "    START --> p_%s\n", nodeID(string(p)))
		}
	}

	// Places: stadium nodes; the live marking is highlighted; colored-token
	// holders get a count badge. A place may name a diagram_group (metadata);
	// same-group places are boxed in a Mermaid subgraph so parallel lanes read
	// as regions. Node declarations must sit inside their subgraph, so declare
	// every place first, then assign classes and wire edges afterwards (class
	// and edge statements are position-independent; subgraphs carry no edges,
	// so the linkStyle indices below are unaffected).
	terminal := terminalPlaces(def)
	placeDecl := func(indent string, p Place) {
		label := humanizeName(string(p))
		if n := tokenCounts[p]; n > 0 {
			label += fmt.Sprintf("<br/><b>⬤×%d</b>", n)
		}
		fmt.Fprintf(&b, "%sp_%s([\"%s\"])\n", indent, nodeID(string(p)), label)
	}

	// Collect groups in first-seen place order for deterministic output.
	var groupOrder []string
	groupPlaces := make(map[string][]Place)
	var ungrouped []Place
	for _, p := range def.AllPlaces() {
		g := ""
		if v, ok := def.PlaceMetadata(p, diagramGroupMeta); ok {
			if s, ok := v.(string); ok {
				g = s
			}
		}
		if g == "" {
			ungrouped = append(ungrouped, p)
			continue
		}
		if _, seen := groupPlaces[g]; !seen {
			groupOrder = append(groupOrder, g)
		}
		groupPlaces[g] = append(groupPlaces[g], p)
	}
	for _, g := range groupOrder {
		fmt.Fprintf(&b, "    subgraph grp_%s [\"%s\"]\n", nodeID(g), escapeMermaidLabel(g))
		for _, p := range groupPlaces[g] {
			placeDecl("        ", p)
		}
		b.WriteString("    end\n")
	}
	for _, p := range ungrouped {
		placeDecl("    ", p)
	}
	for _, p := range def.AllPlaces() {
		id := "p_" + nodeID(string(p))
		switch {
		case current[p]:
			fmt.Fprintf(&b, "    class %s current\n", id)
		case terminal[p]:
			fmt.Fprintf(&b, "    class %s terminal\n", id)
		default:
			fmt.Fprintf(&b, "    class %s place\n", id)
		}
	}

	// Transitions: typed rectangle nodes with guard-labeled routing edges.
	// linkStyle indexes count every edge in the document, so start after
	// the entry-marker edges.
	var timerEdges, resetEdges []int
	edge := len(initial)
	for i := range def.Transitions {
		t := &def.Transitions[i]
		id := "t_" + nodeID(t.Name())

		class := "action"
		if _, timed := t.TimeoutAfter(); timed {
			class = "timer"
		}
		if v, ok := t.Metadata(diagramClassMeta); ok {
			if s, ok := v.(string); ok && s != "" {
				class = escapeMermaidLabel(s)
			}
		}

		label := humanizeName(t.Name())
		if d, ok := t.TimeoutAfter(); ok {
			label = "⏱ " + label + "<br/>after " + escapeMermaidLabel(d.String())
		}
		fmt.Fprintf(&b, "    %s[\"%s\"]\n    class %s %s\n", id, label, id, class)

		// Fork/join gateway diamonds, in the BPMN idiom: the diamond marks a
		// gateway and the word inside it IS the semantics — "all" (AND-join:
		// every input must be marked) or "any" (OR-input/FromAny: exactly
		// one input is consumed). A single input needs no gateway.
		timed := class == "timer"
		countEdge := func() {
			if timed {
				timerEdges = append(timerEdges, edge)
			}
			edge++
		}
		if len(t.From()) > 1 {
			jid := "j_" + nodeID(t.Name())
			word := "all"
			if t.FromAny() {
				word = "any"
			}
			fmt.Fprintf(&b, "    %s{\"%s\"}\n    class %s gateway\n", jid, word, jid)
			for _, from := range t.From() {
				fmt.Fprintf(&b, "    p_%s --> %s\n", nodeID(string(from)), jid)
				countEdge()
			}
			fmt.Fprintf(&b, "    %s --> %s\n", jid, id)
			countEdge()
		} else {
			for _, from := range t.From() {
				fmt.Fprintf(&b, "    p_%s --> %s\n", nodeID(string(from)), id)
				countEdge()
			}
		}

		// A multi-output transition forks through a ◇all gateway — outputs
		// are always "produce all". The guard labels the single trunk edge
		// into the gateway (the guard gates the firing, not one branch).
		// XOR-splits (alternative guarded transitions out of one place) need
		// no gateway: the place is the choice point.
		outLabel := ""
		if g, ok := t.Metadata("guard"); ok {
			if gs, ok := g.(string); ok && gs != "" {
				outLabel = "|\"❰ " + prettifyGuard(gs) + " ❱\"|"
			}
		}
		if len(t.To()) > 1 {
			fid := "f_" + nodeID(t.Name())
			fmt.Fprintf(&b, "    %s{\"all\"}\n    class %s gateway\n", fid, fid)
			fmt.Fprintf(&b, "    %s -->%s %s\n", id, outLabel, fid)
			countEdge()
			for _, to := range t.To() {
				fmt.Fprintf(&b, "    %s --> p_%s\n", fid, nodeID(string(to)))
				countEdge()
			}
		} else {
			for _, to := range t.To() {
				fmt.Fprintf(&b, "    %s -->%s p_%s\n", id, outLabel, nodeID(string(to)))
				countEdge()
			}
		}

		for _, reset := range t.Resets() {
			fmt.Fprintf(&b, "    %s -. cancels .-> p_%s\n", id, nodeID(string(reset)))
			resetEdges = append(resetEdges, edge)
			edge++
		}
	}

	// Neutral, self-contained palette. Hosts may append classDefs of their
	// own and reference them via the diagram_class metadata.
	b.WriteString(`    classDef place fill:#FFFFFF,stroke:#6B7280,stroke-width:1px,color:#111827
    classDef current fill:#DCFCE7,stroke:#15803D,stroke-width:3px,color:#14532D,font-weight:bold
    classDef terminal fill:#F3F4F6,stroke:#6B7280,stroke-dasharray:3 3,color:#374151
    classDef action fill:#1D4ED8,stroke:#1E3A8A,color:#FFFFFF
    classDef person fill:#15803D,stroke:#14532D,color:#FFFFFF
    classDef auto fill:#E0F2FE,stroke:#0369A1,color:#0C4A6E
    classDef timer fill:#FEF3C7,stroke:#B45309,color:#92400E
    classDef startMarker fill:#111827,stroke:#111827,color:#111827
    classDef gateway fill:#F8FAFC,stroke:#334155,stroke-width:2px,color:#334155,font-weight:bold
`)
	for _, i := range timerEdges {
		fmt.Fprintf(&b, "    linkStyle %d stroke:#B45309,stroke-dasharray:6 3\n", i)
	}
	for _, i := range resetEdges {
		fmt.Fprintf(&b, "    linkStyle %d stroke:#B91C1C\n", i)
	}
	return b.String()
}

// terminalPlaces reports the places no transition consumes from — the net's
// end states, rendered with a distinct style.
func terminalPlaces(def *Definition) map[Place]bool {
	consumed := make(map[Place]bool)
	for i := range def.Transitions {
		for _, p := range def.Transitions[i].From() {
			consumed[p] = true
		}
	}
	out := make(map[Place]bool)
	for _, p := range def.AllPlaces() {
		if !consumed[p] {
			out[p] = true
		}
	}
	return out
}
