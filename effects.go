// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"sync"
)

// EffectDecl is one effect bound to a transition in the definition: the name of
// a host-registered implementation plus the parameters it is invoked with.
//
// Declaring effects on the transition — rather than passing closures at every
// call site — is what lets a definition describe not just which state changes
// are legal but what happens when one occurs. Two transitions out of the same
// place can carry different effects, so a branch resolved by ApplyAny fires the
// effects of the branch that actually won.
type EffectDecl struct {
	// Name identifies the implementation registered in the EffectRegistry.
	Name string
	// Params are handed to the implementation verbatim. They are part of the
	// definition's structure and are fingerprinted, so changing a parameter is
	// a definition change (see Definition.Fingerprint).
	Params map[string]any
}

// clone returns a deep-enough copy that callers cannot mutate a definition's
// declared params through the slice they passed in.
func (e EffectDecl) clone() EffectDecl {
	if e.Params == nil {
		return EffectDecl{Name: e.Name}
	}
	params := make(map[string]any, len(e.Params))
	maps.Copy(params, e.Params)
	return EffectDecl{Name: e.Name, Params: params}
}

// record serializes the declaration for the definition fingerprint. Params are
// JSON-encoded, which sorts map keys, so the encoding is stable across runs.
func (e EffectDecl) record() string {
	if len(e.Params) == 0 {
		return e.Name
	}
	b, err := json.Marshal(e.Params)
	if err != nil {
		// A params map that cannot be JSON-encoded (e.g. a func value) cannot
		// be fingerprinted meaningfully; fall back to the name so the record
		// stays deterministic rather than panicking on a definition build.
		return e.Name
	}
	return e.Name + "\x00" + string(b)
}

// EffectEvent is what an effect implementation is told about the firing it is
// reacting to. It is a value snapshot, not a live workflow handle: an effect
// runs while the state transaction is open (or, for an after-commit effect,
// once it has closed), and must not reach back into the instance.
type EffectEvent struct {
	// WorkflowID is the instance the transition fired on.
	WorkflowID string
	// Transition is the name of the transition that fired.
	Transition string
	// Before and After are the marked places either side of this firing.
	Before []Place
	// After is the marking after this firing.
	After []Place
	// Params are the declared parameters for this effect, also passed
	// separately for convenience.
	Params map[string]any
	// Context is a copy of the workflow's context map at save time. Reserved
	// definition keys are already stripped.
	Context map[string]any
}

// EffectFunc is a transactional effect implementation. It runs inside the same
// transaction as the state save, in declared order; returning an error aborts
// the transaction, so neither the state change nor any sibling effect commits.
//
// tx is the backend's own transaction handle — *sql.Tx for the shipped SQL
// backends, whatever a custom backend hands its side effects. Type-assert it.
type EffectFunc func(ctx context.Context, tx any, ev EffectEvent) error

// AfterCommitFunc is an effect that runs only after the state transaction has
// committed successfully — the phase for work that must NOT be in the
// transaction, such as sending mail or calling a third-party API.
//
// AT-LEAST-ONCE. A crash between commit and this call loses the invocation, and
// a caller-level retry can repeat it. Key these effects idempotently or route
// them through an outbox written by a transactional effect. This is the same
// boundary listeners have (see docs/BOUNDARIES.md); the library provides the
// phase, not the guarantee.
type AfterCommitFunc func(ctx context.Context, ev EffectEvent) error

// EffectRegistry maps effect names declared in a definition to their host
// implementations. It is safe for concurrent use; register everything during
// startup and share one registry across every Manager.
type EffectRegistry struct {
	mu          sync.RWMutex
	tx          map[string]EffectFunc
	afterCommit map[string]AfterCommitFunc
}

// NewEffectRegistry returns an empty registry.
func NewEffectRegistry() *EffectRegistry {
	return &EffectRegistry{
		tx:          make(map[string]EffectFunc),
		afterCommit: make(map[string]AfterCommitFunc),
	}
}

// Register binds a transactional effect implementation to a name. Registering
// the same name twice is an error: a silent overwrite would make which
// implementation runs depend on startup order.
func (r *EffectRegistry) Register(name string, fn EffectFunc) error {
	if name == "" {
		return fmt.Errorf("effect name cannot be empty")
	}
	if fn == nil {
		return fmt.Errorf("effect %q: implementation cannot be nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.tx[name]; dup {
		return fmt.Errorf("effect %q is already registered", name)
	}
	r.tx[name] = fn
	return nil
}

// RegisterAfterCommit binds an after-commit implementation to a name. Names are
// in a separate namespace from transactional effects, so the same name may mean
// both — a definition says which phase it wants by where it declares it.
func (r *EffectRegistry) RegisterAfterCommit(name string, fn AfterCommitFunc) error {
	if name == "" {
		return fmt.Errorf("after-commit effect name cannot be empty")
	}
	if fn == nil {
		return fmt.Errorf("after-commit effect %q: implementation cannot be nil", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.afterCommit[name]; dup {
		return fmt.Errorf("after-commit effect %q is already registered", name)
	}
	r.afterCommit[name] = fn
	return nil
}

// MustRegister is Register, panicking on error. For package-level wiring where
// a duplicate name is a programming mistake, not a runtime condition.
func (r *EffectRegistry) MustRegister(name string, fn EffectFunc) {
	if err := r.Register(name, fn); err != nil {
		panic(err)
	}
}

// MustRegisterAfterCommit is RegisterAfterCommit, panicking on error.
func (r *EffectRegistry) MustRegisterAfterCommit(name string, fn AfterCommitFunc) {
	if err := r.RegisterAfterCommit(name, fn); err != nil {
		panic(err)
	}
}

func (r *EffectRegistry) lookupTx(name string) (EffectFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.tx[name]
	return fn, ok
}

func (r *EffectRegistry) lookupAfterCommit(name string) (AfterCommitFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.afterCommit[name]
	return fn, ok
}

// Validate reports every effect a definition declares that this registry cannot
// resolve. Call it at startup: without it, an unregistered effect surfaces only
// when the transition first fires, which for a rare branch can be much later.
func (r *EffectRegistry) Validate(def *Definition) error {
	if def == nil {
		return nil
	}
	var missing []string
	for i := range def.Transitions {
		t := &def.Transitions[i]
		for _, e := range t.Effects() {
			if _, ok := r.lookupTx(e.Name); !ok {
				missing = append(missing, fmt.Sprintf("%s: effect %q", t.Name(), e.Name))
			}
		}
		for _, e := range t.AfterCommit() {
			if _, ok := r.lookupAfterCommit(e.Name); !ok {
				missing = append(missing, fmt.Sprintf("%s: after_commit %q", t.Name(), e.Name))
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("unregistered effects: %v", missing)
}

// pendingAfterCommit is a resolved after-commit effect awaiting a successful
// commit: the implementation plus the event it will be handed.
type pendingAfterCommit struct {
	fn AfterCommitFunc
	ev EffectEvent
}

// resolveEffects turns the transitions that fired into concrete effect
// closures. Both phases are resolved BEFORE the save so an unregistered name
// fails the whole attempt rather than aborting midway through a transaction
// with some effects already applied.
//
// Effects are returned in firing order, and within a firing in declared order.
func (m *Manager) resolveEffects(
	id string,
	def *Definition,
	steps []FiredStep,
	ctxData map[string]any,
) ([]TxSideEffect, []pendingAfterCommit, error) {
	if len(steps) == 0 {
		return nil, nil, nil
	}
	var txEffects []TxSideEffect
	var pending []pendingAfterCommit

	for _, step := range steps {
		t := def.Transition(step.Transition)
		if t == nil {
			// The transition fired, so it exists in this definition; a nil here
			// would mean the definition changed under us mid-Execute.
			return nil, nil, fmt.Errorf("effects: transition %q not found in definition", step.Transition)
		}
		for _, decl := range t.Effects() {
			fn, ok := m.effects.lookupTx(decl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("effects: transition %q declares unregistered effect %q", step.Transition, decl.Name)
			}
			ev := newEffectEvent(id, step, decl, ctxData)
			txEffects = append(txEffects, func(ctx context.Context, tx any) error {
				return fn(ctx, tx, ev)
			})
		}
		for _, decl := range t.AfterCommit() {
			fn, ok := m.effects.lookupAfterCommit(decl.Name)
			if !ok {
				return nil, nil, fmt.Errorf("effects: transition %q declares unregistered after_commit effect %q", step.Transition, decl.Name)
			}
			pending = append(pending, pendingAfterCommit{fn: fn, ev: newEffectEvent(id, step, decl, ctxData)})
		}
	}
	return txEffects, pending, nil
}

// newEffectEvent snapshots what an effect implementation is told. Slices and
// maps are copied so an effect cannot mutate the marking the save is about to
// persist, or another effect's view of the context.
func newEffectEvent(id string, step FiredStep, decl EffectDecl, ctxData map[string]any) EffectEvent {
	ev := EffectEvent{
		WorkflowID: id,
		Transition: step.Transition,
		Before:     slices.Clone(step.Before),
		After:      slices.Clone(step.After),
		Params:     decl.clone().Params,
	}
	if ctxData != nil {
		ev.Context = make(map[string]any, len(ctxData))
		maps.Copy(ev.Context, ctxData)
	}
	return ev
}

// runAfterCommit invokes resolved after-commit effects in order, stopping at
// the first error. The state change has already committed and is NOT undone —
// the error only tells the caller an after-commit effect failed.
func runAfterCommit(ctx context.Context, pending []pendingAfterCommit) error {
	for _, p := range pending {
		if err := p.fn(ctx, p.ev); err != nil {
			return fmt.Errorf("after_commit effect for transition %q: %w", p.ev.Transition, err)
		}
	}
	return nil
}

// definitionDeclaresEffects reports whether any transition binds an effect. The
// Manager needs this before running the fire loop, to decide up front whether
// transactional storage is required — the same check it makes for
// WithTxSideEffect, but knowable from the definition alone.
func definitionDeclaresEffects(def *Definition) bool {
	if def == nil {
		return false
	}
	for i := range def.Transitions {
		if len(def.Transitions[i].Effects()) > 0 {
			return true
		}
	}
	return false
}
