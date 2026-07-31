// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"fmt"
	"maps"
	"math"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// RequirementSpec declares a dynamic-cardinality join: a transition input whose
// arity is resolved at fire time from an expression rather than fixed by the
// definition.
//
// The classic AND-join is structural — "every declared input place is marked" —
// so the number of things a transition waits for is a property of the drawing.
// The recurring business-workflow shape is not: an approval chain's length comes
// from the record's value, so the transition must wait for a set whose size is
// only known at runtime. A requirement expresses exactly that, over the tokens
// resting in one input place.
//
//	require:
//	  - place: submitted
//	    where: "token.role in required_roles"   # which tokens count
//	    distinct: role                          # two tokens of one role are one approval
//	    count: "len(required_roles)"            # how many are needed, resolved now
//
// A requirement is BOTH an enablement condition and a token selector: the
// transition is enabled only when its place holds enough matching tokens, and
// firing consumes exactly the tokens it selected, leaving the remainder in
// place. See Transition.SetRequirements for the full firing rules.
type RequirementSpec struct {
	// Place is the input place whose tokens are counted. It must be one of the
	// transition's own 'from' places (NewDefinition validates this), and at most
	// one requirement may target a given place.
	Place Place

	// Count is an expression yielding the number of tokens required. It is
	// evaluated at fire time against the workflow context, plus `tokens` — the
	// data of every token currently at Place. It must yield a non-negative
	// integer (a JSON-decoded float64 with no fractional part is accepted, since
	// context values round-trip through storage).
	Count string

	// Where, when set, is an expression evaluated once per token at Place to
	// decide whether that token counts toward Count. The token's data is exposed
	// as `token`; the workflow context is in scope. An empty Where counts every
	// token in the place.
	Where string

	// Distinct, when set, names a token-data field that must be UNIQUE among the
	// counted tokens: one token per distinct value, first occurrence wins. It is
	// what makes "two approvals from the same role are one approval" structural
	// rather than a check the host has to remember to write. Tokens that do not
	// carry the field are never counted.
	Distinct string
}

// Requirement is a compiled RequirementSpec. Build one with NewRequirement; it
// is immutable and safe to share across workflow instances.
type Requirement struct {
	spec     RequirementSpec
	countPrg *vm.Program
	wherePrg *vm.Program
}

// NewRequirement compiles a requirement declaration, reporting an expression
// that does not parse. Compiling at definition-build time rather than at fire
// time means a malformed count expression fails at startup, not on the first
// firing of a rare branch.
func NewRequirement(spec RequirementSpec) (Requirement, error) {
	if spec.Place == "" {
		return Requirement{}, fmt.Errorf("%w: requirement place cannot be empty", ErrInvalidTransition)
	}
	if strings.TrimSpace(spec.Count) == "" {
		return Requirement{}, fmt.Errorf("%w: requirement on place %q has no count expression", ErrInvalidTransition, spec.Place)
	}

	r := Requirement{spec: spec}
	var err error
	if r.countPrg, err = expr.Compile(spec.Count); err != nil {
		return Requirement{}, fmt.Errorf("%w: requirement on place %q: count %q: %w",
			ErrInvalidExpression, spec.Place, spec.Count, err)
	}
	if spec.Where != "" {
		if r.wherePrg, err = expr.Compile(spec.Where); err != nil {
			return Requirement{}, fmt.Errorf("%w: requirement on place %q: where %q: %w",
				ErrInvalidExpression, spec.Place, spec.Where, err)
		}
	}
	return r, nil
}

// MustNewRequirement is NewRequirement, panicking on error. For declaring
// definitions in Go, where a malformed expression is a programming mistake.
func MustNewRequirement(spec RequirementSpec) Requirement {
	r, err := NewRequirement(spec)
	if err != nil {
		panic(err)
	}
	return r
}

// Spec returns the declaration this requirement was compiled from.
func (r Requirement) Spec() RequirementSpec { return r.spec }

// Place returns the input place whose tokens this requirement counts.
func (r Requirement) Place() Place { return r.spec.Place }

// String renders the requirement in the canonical form used by diagrams and by
// the ErrNotEnabled message, e.g.
// `submitted >= len(required_roles) distinct by role where token.role in required_roles`.
func (r Requirement) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s >= %s", r.spec.Place, r.spec.Count)
	if r.spec.Distinct != "" {
		fmt.Fprintf(&b, " distinct by %s", r.spec.Distinct)
	}
	if r.spec.Where != "" {
		fmt.Fprintf(&b, " where %s", r.spec.Where)
	}
	return b.String()
}

// record serializes the requirement for the definition fingerprint. Every field
// is structural: changing which tokens count, how many, or how they are
// de-duplicated changes when the net can fire.
func (r Requirement) record() string {
	var b strings.Builder
	writeLenPrefixed(&b, string(r.spec.Place))
	writeLenPrefixed(&b, r.spec.Count)
	writeLenPrefixed(&b, r.spec.Where)
	writeLenPrefixed(&b, r.spec.Distinct)
	return b.String()
}

// selectTokens evaluates the requirement against the tokens currently at its
// place and returns the INDEXES (into tokens, in the given order) of those a
// firing would consume. met reports whether the place holds enough matching
// tokens; an error means the declaration could not be evaluated at all — a
// broken expression or a count that is not a non-negative integer — which is a
// definition/environment fault, not a "not yet" condition.
//
// Selection is deterministic: matching tokens are taken in the order the place
// holds them, so two evaluations of the same marking consume the same tokens.
func (r Requirement) selectTokens(tokens []Token, ctxData map[string]any) (picked []int, met bool, err error) {
	need, err := r.requiredCount(tokens, ctxData)
	if err != nil {
		return nil, false, err
	}

	env := requirementEnv(tokens, ctxData)
	seen := make(map[string]bool)
	for i := range tokens {
		if len(picked) == need {
			break
		}
		ok, err := r.matches(tokens[i], env)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		if r.spec.Distinct != "" {
			v, has := tokens[i].Get(r.spec.Distinct)
			if !has {
				continue // cannot be told apart; never counts
			}
			key := distinctKey(v)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		picked = append(picked, i)
	}

	if len(picked) < need {
		return nil, false, nil
	}
	return picked, true, nil
}

// matches evaluates the where expression for one token. A requirement without a
// where clause counts every token.
func (r Requirement) matches(tok Token, env map[string]any) (bool, error) {
	if r.wherePrg == nil {
		return true, nil
	}
	data := tok.Data()
	if data == nil {
		// An uncolored presence token carries no data. Expose an empty map so
		// `token.field` evaluates to nil rather than faulting on a nil map.
		data = TokenData{}
	}
	env["token"] = data
	out, err := expr.Run(r.wherePrg, env)
	if err != nil {
		return false, fmt.Errorf("%w: requirement on place %q: where %q: %w",
			ErrInvalidExpression, r.spec.Place, r.spec.Where, err)
	}
	b, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("%w: requirement on place %q: where %q must return boolean, got %T",
			ErrInvalidExpression, r.spec.Place, r.spec.Where, out)
	}
	return b, nil
}

// requiredCount evaluates the count expression to a non-negative integer.
func (r Requirement) requiredCount(tokens []Token, ctxData map[string]any) (int, error) {
	out, err := expr.Run(r.countPrg, requirementEnv(tokens, ctxData))
	if err != nil {
		return 0, fmt.Errorf("%w: requirement on place %q: count %q: %w",
			ErrInvalidExpression, r.spec.Place, r.spec.Count, err)
	}
	n, ok := toInt(out)
	if !ok {
		return 0, fmt.Errorf("%w: requirement on place %q: count %q must return an integer, got %T",
			ErrInvalidExpression, r.spec.Place, r.spec.Count, out)
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: requirement on place %q: count %q must not be negative, got %d",
			ErrInvalidExpression, r.spec.Place, r.spec.Count, n)
	}
	return n, nil
}

// requirementEnv builds the evaluation environment: the workflow context, plus
// `tokens` — the data of every token currently at the requirement's place. The
// reserved names `tokens` and `token` shadow context keys of the same name, as
// they do in guard expressions.
func requirementEnv(tokens []Token, ctxData map[string]any) map[string]any {
	env := make(map[string]any, len(ctxData)+2)
	maps.Copy(env, ctxData)
	datas := make([]TokenData, len(tokens))
	for i, t := range tokens {
		datas[i] = t.Data()
	}
	env["tokens"] = datas
	return env
}

// toInt coerces an expression result to an int. Whole float64 values are
// accepted because context values that have round-tripped through JSON storage
// come back as float64 — rejecting them would make a requirement work before a
// save and fail after one.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int8:
		return int(n), true
	case int16:
		return int(n), true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case uint:
		return fromUint(uint64(n))
	case uint8:
		return int(n), true
	case uint16:
		return int(n), true
	case uint32:
		return fromUint(uint64(n))
	case uint64:
		return fromUint(n)
	case float32:
		return wholeFloat(float64(n))
	case float64:
		return wholeFloat(n)
	default:
		return 0, false
	}
}

// fromUint converts an unsigned value that fits in an int. A count larger than
// the platform's int is not a count anyone meant to write.
func fromUint(n uint64) (int, bool) {
	if n > math.MaxInt {
		return 0, false
	}
	return int(n), true
}

// wholeFloat converts a float to an int only when it has no fractional part: a
// count of 2.5 is a mistake worth reporting, not something to round.
func wholeFloat(f float64) (int, bool) {
	n := int(f)
	if float64(n) != f {
		return 0, false
	}
	return n, true
}

// selectRequirementsLocked evaluates every requirement of t against the current
// marking, returning for each requirement's place the indexes of the tokens a
// firing would consume (nil when t declares no requirements).
//
// It is the single decision point for both halves of the feature: an unmet
// requirement is reported as ErrNotEnabled, and the indexes it returns are the
// consume set moveMarking will act on. Callers MUST hold w.mu — the returned
// indexes are only valid for the marking as it stands under that same hold.
func (w *Workflow) selectRequirementsLocked(t *Transition) (map[Place][]int, error) {
	if len(t.requires) == 0 {
		return nil, nil
	}
	picked := make(map[Place][]int, len(t.requires))
	for i := range t.requires {
		r := t.requires[i]
		idx, met, err := r.selectTokens(w.marking.TokensAt(r.spec.Place), w.context)
		if err != nil {
			return nil, fmt.Errorf("transition %q: %w", t.name, err)
		}
		if !met {
			return nil, fmt.Errorf("%w: transition %q requires %s", ErrNotEnabled, t.name, r)
		}
		picked[r.spec.Place] = idx
	}
	return picked, nil
}

// requirementsMet is the enablement half of selectRequirementsLocked for
// callers that do NOT already hold w.mu. The answer is a snapshot: a firing
// re-evaluates under the write lock before it consumes anything.
func (w *Workflow) requirementsMet(t *Transition) error {
	if len(t.requires) == 0 {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, err := w.selectRequirementsLocked(t)
	return err
}

// distinctKey renders a token-data value as a de-duplication key. The type is
// part of the key so that values of different types (the string "1" and the
// number 1) are never conflated.
func distinctKey(v any) string {
	return fmt.Sprintf("%T\x00%v", v, v)
}
