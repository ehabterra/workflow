// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"fmt"
	"time"
)

// Transition represents a transition between places in the workflow
type Transition struct {
	name        string
	from        []Place
	to          []Place
	metadata    map[string]any
	constraints []Constraint

	// timeout, when positive, marks this transition as time-driven: it becomes
	// "due" once its input tokens have waited this long (see the Due API and
	// Manager.FireDue). It is a duration, not a wall-clock deadline — the deadline
	// is computed per instance from the token's entry time — so the same
	// definition drives every instance's timer. It is the single source of truth
	// for the timeout; a zero value means the transition is not timed. The library
	// never fires it on its own; a host schedules the check.
	timeout time.Duration

	// fromAny, when true, makes this an OR-input (merge) transition: it is
	// enabled when ANY ONE of its input places is marked, and firing consumes
	// exactly the first marked input (declaration order) — the other inputs
	// are left untouched. The default (false) is the Petri-net AND-join: all
	// inputs must be marked and all are consumed.
	//
	// The canonical use is "the same action from either stage": approve from
	// pending OR escalated collapses two near-identical transitions into one.
	fromAny bool

	// resets holds the places this transition CLEARS when it fires — reset
	// arcs, the Petri-net primitive for cancellation regions. Firing removes
	// every token from each reset place atomically with the marking move:
	// inputs are consumed, reset places are emptied, and only then are
	// outputs produced (so a place that is both reset and an output keeps
	// what this firing produces). Reset places do not affect enablement.
	//
	// The canonical use is an OR-outcome AND-split: a reject transition on
	// one review branch resets the sibling branch's places, cancelling its
	// pending work — and any timer running on those tokens — in the same
	// atomic firing.
	resets []Place

	// requires holds the dynamic-cardinality joins over this transition's input
	// places: enablement conditions whose arity is resolved at fire time rather
	// than fixed by the arc structure. See SetRequirements.
	requires []Requirement

	// effects holds the transactional effects this transition declares, in
	// execution order, resolved against the Manager's EffectRegistry when the
	// transition fires. They commit with the state change.
	effects []EffectDecl

	// afterCommit holds effects that run only once the state transaction has
	// committed — at-least-once, for work that must not be transactional.
	afterCommit []EffectDecl
}

// Constraint represents a validation constraint for a transition
type Constraint interface {
	Validate(Event) error
}

// NewTransition creates a new transition
func NewTransition(name string, from []Place, to []Place) (*Transition, error) {
	if name == "" {
		return nil, fmt.Errorf("transition name cannot be empty")
	}

	if len(from) == 0 {
		return nil, fmt.Errorf("transition must have at least one 'from' place")
	}

	if len(to) == 0 {
		return nil, fmt.Errorf("transition must have at least one 'to' place")
	}

	// Check for duplicate places in from
	fromSet := make(map[Place]bool)
	for _, place := range from {
		if fromSet[place] {
			return nil, fmt.Errorf("duplicate 'from' place: %s", place)
		}
		fromSet[place] = true
	}

	// Check for duplicate places in to
	toSet := make(map[Place]bool)
	for _, place := range to {
		if toSet[place] {
			return nil, fmt.Errorf("duplicate 'to' place: %s", place)
		}
		toSet[place] = true
	}

	return &Transition{
		name:        name,
		from:        from,
		to:          to,
		metadata:    make(map[string]any),
		constraints: make([]Constraint, 0),
	}, nil
}

// Name returns the transition name
func (t *Transition) Name() string {
	return t.name
}

// From returns the source places of the transition
func (t *Transition) From() []Place {
	// Return a copy to prevent external modification
	fromCopy := make([]Place, len(t.from))
	copy(fromCopy, t.from)
	return fromCopy
}

// To returns the target places of the transition
func (t *Transition) To() []Place {
	// Return a copy to prevent external modification
	toCopy := make([]Place, len(t.to))
	copy(toCopy, t.to)
	return toCopy
}

// SetTimeoutAfter marks the transition as time-driven: it becomes due once its
// input tokens have waited for d (measured from when they entered their place).
// The duration is stored on the transition itself as the single source of truth
// (diagrams and tooling read it back via TimeoutAfter). A non-positive d clears
// the timeout.
//
// The engine never fires a due transition by itself — a host drives the clock
// (see Workflow.Due, Workflow.NextDue, and Manager.FireDue).
//
// Like the rest of a Definition, a transition's timeout is expected to be
// configured before any Workflow is created from the definition; mutating it
// afterward is not safe against concurrent Workflow.Due/NextDue reads on
// workflows that share the definition.
func (t *Transition) SetTimeoutAfter(d time.Duration) {
	if d <= 0 {
		t.timeout = 0
		return
	}
	t.timeout = d
}

// TimeoutAfter returns the transition's timeout duration and whether one is set
// (a positive timeout). The timeout field is authoritative; there is no metadata
// mirror.
func (t *Transition) TimeoutAfter() (time.Duration, bool) {
	return t.timeout, t.timeout > 0
}

// SetFromAny toggles OR-input (merge) semantics: enabled when any one input
// place is marked; firing consumes exactly the first marked input in
// declaration order. FromAny is part of the definition's Fingerprint.
func (t *Transition) SetFromAny(v bool) { t.fromAny = v }

// FromAny reports whether this transition uses OR-input (merge) semantics.
func (t *Transition) FromAny() bool { return t.fromAny }

// consumeSet returns the input places a firing of t would consume, given a
// marked-place predicate: every input for the default AND-join, or the first
// marked input for an OR-input (FromAny) transition. ok is false when the
// marking does not enable t.
func (t *Transition) consumeSet(marked func(Place) bool) ([]Place, bool) {
	if t.fromAny {
		for _, p := range t.from {
			if marked(p) {
				return []Place{p}, true
			}
		}
		return nil, false
	}
	for _, p := range t.from {
		if !marked(p) {
			return nil, false
		}
	}
	return t.From(), true
}

// SetResets declares the places this transition clears when it fires (reset
// arcs / a cancellation region). Duplicates are collapsed; order is not
// significant. Reset places must exist in the definition — NewDefinition
// validates them. Resets are part of the definition's Fingerprint.
func (t *Transition) SetResets(places ...Place) {
	seen := make(map[Place]bool, len(places))
	t.resets = t.resets[:0]
	for _, p := range places {
		if !seen[p] {
			seen[p] = true
			t.resets = append(t.resets, p)
		}
	}
}

// Resets returns the places this transition clears when it fires. The returned
// slice is a copy.
func (t *Transition) Resets() []Place {
	if len(t.resets) == 0 {
		return nil
	}
	out := make([]Place, len(t.resets))
	copy(out, t.resets)
	return out
}

// SetRequirements declares dynamic-cardinality joins over this transition's
// input places — the "wait for a set whose size is only known at runtime"
// primitive. Requirements are part of the definition's Fingerprint.
//
// A requirement does two things:
//
//   - ENABLEMENT. On top of the structural check (its place must be marked),
//     the place must hold at least `count` tokens matching `where`, counted
//     `distinct` by a field when one is named. An unmet requirement is
//     ErrNotEnabled, so an ApplyAny candidate simply loses to its sibling.
//   - CONSUMPTION. Firing consumes EXACTLY the tokens the requirement selected
//     and LEAVES THE REMAINDER in the place. This differs from an ordinary input
//     place, which is drained entirely. Only the selected tokens are carried to
//     the outputs.
//
// Two deliberate restrictions, both enforced by NewDefinition, because each
// would mean two competing token selectors on one transition:
//
//   - a requirement's place must be one of the transition's own inputs, and at
//     most one requirement may target a given place;
//   - a transition may not combine requirements with FromAny.
//
// Per-token firing (ApplyTransitionForToken) is likewise rejected on a
// transition with requirements: the requirement IS the token selection.
//
// Reset arcs are unchanged and still clear WHOLE places, so a place that is both
// required and reset ends up empty regardless of what the requirement selected.
// Requirements are not evaluated by the Due API — like guards, they are honored
// when the transition actually fires.
func (t *Transition) SetRequirements(reqs ...Requirement) {
	if len(reqs) == 0 {
		t.requires = nil
		return
	}
	t.requires = make([]Requirement, len(reqs))
	copy(t.requires, reqs)
}

// Requirements returns the transition's dynamic-cardinality joins. The returned
// slice is a copy; the requirements themselves are immutable.
func (t *Transition) Requirements() []Requirement {
	if len(t.requires) == 0 {
		return nil
	}
	out := make([]Requirement, len(t.requires))
	copy(out, t.requires)
	return out
}

// AddConstraint adds a constraint to the transition
func (t *Transition) AddConstraint(constraint Constraint) {
	t.constraints = append(t.constraints, constraint)
}

// SetEffects declares the transactional effects this transition fires, in
// execution order. They run inside the state-save transaction (see EffectFunc);
// resolving them needs a Manager built WithEffectRegistry.
//
// Declaring effects here rather than passing closures per call is what lets two
// transitions out of the same place carry different effects, so an ApplyAny
// branch fires exactly the effects of the branch that won.
func (t *Transition) SetEffects(effects ...EffectDecl) {
	t.effects = cloneEffectDecls(effects)
}

// Effects returns the declared transactional effects, in declared order.
func (t *Transition) Effects() []EffectDecl { return cloneEffectDecls(t.effects) }

// SetAfterCommit declares effects that run only after the state transaction has
// committed — the phase for work that must not be transactional. They are
// AT-LEAST-ONCE; see AfterCommitFunc.
func (t *Transition) SetAfterCommit(effects ...EffectDecl) {
	t.afterCommit = cloneEffectDecls(effects)
}

// AfterCommit returns the declared after-commit effects, in declared order.
func (t *Transition) AfterCommit() []EffectDecl { return cloneEffectDecls(t.afterCommit) }

// cloneEffectDecls copies a declaration slice so neither the caller nor a
// reader can mutate a definition's declared effects in place.
func cloneEffectDecls(in []EffectDecl) []EffectDecl {
	if len(in) == 0 {
		return nil
	}
	out := make([]EffectDecl, len(in))
	for i, e := range in {
		out[i] = e.clone()
	}
	return out
}

// SetMetadata sets metadata for the transition
func (t *Transition) SetMetadata(key string, value any) {
	t.metadata[key] = value
}

// Metadata returns the value for the given key from the transition metadata
func (t *Transition) Metadata(key string) (any, bool) {
	value, ok := t.metadata[key]
	return value, ok
}

// needsTx reports whether any of this transition's constraints must be
// evaluated inside the firing transaction (see TxScopedConstraint).
func (t *Transition) needsTx() bool {
	for _, c := range t.constraints {
		if tc, ok := c.(TxScopedConstraint); ok && tc.NeedsTx() {
			return true
		}
	}
	return false
}

// validate validates the transition against all constraints (internal method)
func (t *Transition) validate(event Event) error {
	for _, constraint := range t.constraints {
		if err := constraint.Validate(event); err != nil {
			return err
		}
	}
	return nil
}

// MustNewTransition is a helper that creates a new transition and panics on error.
// This is useful for defining transitions in a declarative way.
func MustNewTransition(name string, from []Place, to []Place) *Transition {
	t, err := NewTransition(name, from, to)
	if err != nil {
		panic(err)
	}
	return t
}
