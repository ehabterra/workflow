// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

// Workflow represents a workflow instance
type Workflow struct {
	name          string
	definition    *Definition
	initialPlaces []Place
	marking       Marking
	context       map[string]any

	// listeners holds this instance's listeners. It carries its own lock, so
	// listeners can be added or removed while transitions fire.
	listeners listenerSet

	manager *Manager // pointer to manager, may be nil
	mu      sync.RWMutex

	// now is the clock used to stamp tokens as they enter a place. It defaults to
	// time.Now and is only ever consulted for a definition with timed transitions;
	// tests (and Manager.FireDue) inject a fixed clock so stamping is deterministic.
	now func() time.Time

	// version is the optimistic-concurrency version last loaded from or saved to
	// a VersionedStorage backend. It is 0 for a workflow that has never been
	// persisted, and ignored by non-versioned backends.
	version int64

	// fired logs the transitions applied to this instance, in order, with the
	// marking either side of each. Manager.Execute drains it after the caller's
	// function returns to resolve the effects those transitions declare.
	//
	// It is per-instance and Execute loads a fresh instance on every conflict
	// retry, so a retried attempt starts with an empty log — the effects of an
	// abandoned attempt can never leak into the one that commits.
	fired []FiredStep
}

// recordFired appends a firing to the log. Callers MUST already hold w.mu:
// it is called from the firing paths between moveMarking and the after-event,
// while the lock is held (the lock is NOT reentrant — see the caution in
// CLAUDE.md).
func (w *Workflow) recordFired(transition string, before, after []Place) {
	w.fired = append(w.fired, FiredStep{Transition: transition, Before: before, After: after})
}

// drainFired returns the firings logged since the last drain and clears the log.
func (w *Workflow) drainFired() []FiredStep {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.fired
	w.fired = nil
	return out
}

// Version returns the workflow's current optimistic-concurrency version. It is 0
// until the workflow has been persisted to a VersionedStorage backend.
func (w *Workflow) Version() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.version
}

// setVersion updates the cached concurrency version (used by Manager after a
// versioned load or save).
func (w *Workflow) setVersion(v int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.version = v
}

// setClock pins the token-stamping clock (used by Manager.FireDue so tokens
// produced while firing due transitions are stamped with the same evaluation
// time the host passed in). A nil clock is ignored.
func (w *Workflow) setClock(now func() time.Time) {
	if now == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.now = now
}

// Storage defines the interface for persisting workflow state. It is
// responsible for loading and saving the workflow's marking, its context data,
// and its optimistic-concurrency version.
//
// Versioning is not optional: every row carries a monotonically increasing
// version, a load returns it, and a save succeeds only if the stored version
// still matches — two writers racing to save the same workflow can never
// silently clobber each other; the loser gets ErrConflict. (An unversioned
// backend would lose updates by design, so the contract does not allow one.)
//
// The marking and the version MUST come from one atomic snapshot: reading them
// separately allows a concurrent commit to pair a stale marking with the new
// version, which turns the version check into a lost update. The storagetest
// conformance suite verifies this.
//
// Every method takes a context.Context so callers can apply cancellation and
// deadlines; implementations are expected to honor it (e.g. by using the
// database/sql *Context methods).
type Storage interface {
	// LoadState loads the workflow's marking, context data, and current version
	// in one atomic snapshot. A never-saved workflow returns ErrWorkflowNotFound.
	LoadState(ctx context.Context, id string) (marking Marking, context map[string]any, version int64, err error)

	// SaveState saves the workflow only if the stored version equals
	// expectedVersion, returning the new (incremented) version on success. Pass
	// expectedVersion 0 to create a new workflow. A mismatch — because another
	// writer saved first, or the row already exists — returns ErrConflict.
	//
	// The full marking is persisted, so data-carrying (colored) tokens
	// round-trip; simple boolean workflows serialize to the compact place-array
	// form. Implementations must persist the ENTIRE context map, so every key
	// set via SetContext survives a save/load round-trip (JSON-encoded values
	// may come back with adjusted types, e.g. numbers as float64). Silently
	// persisting only a subset of keys is a contract violation; the storagetest
	// conformance suite checks this.
	SaveState(ctx context.Context, id string, marking Marking, context map[string]any, expectedVersion int64) (newVersion int64, err error)

	// DeleteState removes the workflow state for the given ID.
	DeleteState(ctx context.Context, id string) error
}

// ListOptions controls pagination for enumerating persisted workflow IDs.
// A zero Limit means no limit.
type ListOptions struct {
	Limit  int
	Offset int
}

// ListableStorage is an optional interface a Storage backend may implement to
// enumerate the IDs of persisted workflows. It is the primitive a host needs to
// scan the fleet — for example a cron sweeping instances awaiting a deadline —
// without dropping to raw SQL. The Manager exposes it via ListWorkflowIDs.
type ListableStorage interface {
	Storage

	// ListIDs returns persisted workflow IDs ordered by ID for stable pagination.
	ListIDs(ctx context.Context, opts ListOptions) ([]string, error)
}

// PlacedToken is one token at rest in one place of one workflow instance — a
// row of the cross-instance token read-model (see TokenQueryStorage).
type PlacedToken struct {
	WorkflowID string
	Place      Place
	Token      Token
}

// TokenQueryStorage is an optional interface a Storage backend may implement
// to answer cross-instance token queries: "every token currently resting in
// place P, across ALL workflow instances". It is the read-model a shared
// token-pool needs (e.g. a batch run listing every payable expense in the
// system) — a single indexed query instead of loading and inspecting every
// instance. The Manager exposes it via ListPlaceTokens.
//
// Backends that normalize each token into its own row (the SQL backends'
// token table) answer this directly; a backend persisting markings as opaque
// blobs cannot, and simply does not implement the interface.
type TokenQueryStorage interface {
	Storage

	// ListPlaceTokens returns every token in the given place across all
	// workflow instances, in stable storage order for pagination. A zero
	// opts.Limit means no limit.
	ListPlaceTokens(ctx context.Context, place Place, opts ListOptions) ([]PlacedToken, error)
}

// TxSideEffect is a write executed atomically with a state save — typically an
// audit/history record or an outbox row. The tx argument is backend-specific:
// the SQL backends pass a *sql.Tx. An effect that returns an error aborts the
// whole transaction, so the state change and the effect either both commit or
// both roll back.
type TxSideEffect func(ctx context.Context, tx any) error

// TransactionalStorage is an optional interface a Storage backend may implement
// to compose a state save with additional writes in one atomic transaction. It
// is what makes "fire a transition + append its history record"
// crash-consistent: a process dying between the two can never leave the state
// and the audit log disagreeing. Manager.Execute uses it when side effects are
// registered via WithTxSideEffect.
type TransactionalStorage interface {
	Storage

	// SaveStateInTx behaves like SaveState but runs the save and every side
	// effect inside a single transaction, committing only if all succeed.
	// Effects run in order after the state write; each receives the
	// backend-specific transaction handle.
	SaveStateInTx(ctx context.Context, id string, marking Marking, context map[string]any, expectedVersion int64, effects ...TxSideEffect) (newVersion int64, err error)
}

// DueStorage is an optional interface a Storage backend may implement to
// maintain a per-instance "next due" index and scan the fleet for instances
// whose deadline has elapsed. It is the storage primitive behind host-driven
// timers (M4): a host cron finds the due instances with ListDue and advances
// them with Manager.FireDue, turning a fleet-wide deadline ("escalate if not
// approved in 3 days") into a single indexed query instead of loading and
// inspecting every instance.
//
// Storage never interprets time itself: the next-due wall-clock is computed by
// the Manager from the workflow definition (which storage does not know) via
// the marking's token entry times, and handed to storage on every save. A nil
// due means no timer is currently running for the instance — the backend
// stores SQL NULL and such an instance never matches ListDue.
//
// The fleet-timer model is inherently multi-writer (many hosts may scan the
// same fleet); Storage's built-in optimistic concurrency is what keeps
// concurrent FireDue calls from clobbering each other.
type DueStorage interface {
	Storage

	// SaveStateWithDue behaves like SaveState but also records the instance's
	// next-due time (nil clears it, so a workflow that reaches a timer-free
	// state drops out of ListDue). The Manager calls it in place of SaveState
	// so the due index is maintained on every save.
	SaveStateWithDue(ctx context.Context, id string, marking Marking, context map[string]any, expectedVersion int64, due *time.Time) (newVersion int64, err error)

	// ListDue returns the IDs of instances whose stored next-due time is
	// non-null and at or before `before`, ordered by due time ascending (then by
	// ID) for stable pagination. A zero limit means no limit. Instances with no
	// running timer (NULL due) are never returned.
	ListDue(ctx context.Context, before time.Time, limit int) ([]string, error)
}

// TransactionalDueStorage composes a due-aware save with atomic side effects —
// the transactional-path counterpart of DueStorage.SaveStateWithDue. A backend
// that is both a TransactionalStorage and a DueStorage MUST implement it so
// Manager.Execute keeps the due index current even when it commits state
// together with a history/outbox effect. Manager.Execute requires it: calling
// Execute with a WithTxSideEffect option against a timed definition on a
// backend that is a DueStorage but not a TransactionalDueStorage is rejected
// (errors.ErrUnsupported), because committing state and effect without updating
// the due column in the same transaction would silently corrupt the index.
type TransactionalDueStorage interface {
	DueStorage
	TransactionalStorage

	// SaveStateInTxWithDue behaves like SaveStateInTx but also records the
	// instance's next-due time (nil clears it), so the state change, the
	// due-index update, and every side effect commit or roll back together.
	SaveStateInTxWithDue(ctx context.Context, id string, marking Marking, context map[string]any, expectedVersion int64, due *time.Time, effects ...TxSideEffect) (newVersion int64, err error)
}

// NewWorkflow creates a workflow instance starting at initialPlace.
//
// Every workflow's marking is a Colored Petri Net marking; a plain workflow just
// uses uncolored tokens (boolean presence). Reach for the token methods
// (CreateToken, GetTokens, ...) only when you need data-carrying tokens.
func NewWorkflow(name string, definition *Definition, initialPlace Place, opts ...WorkflowOption) (*Workflow, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidWorkflow)
	}

	if definition == nil {
		return nil, fmt.Errorf("%w: definition cannot be nil", ErrInvalidDefinition)
	}

	if !definition.Place(initialPlace) {
		return nil, fmt.Errorf("%w: initial place %s is not defined in the workflow", ErrInvalidPlace, initialPlace)
	}

	return newWorkflow(name, definition, NewMarking([]Place{initialPlace}), opts...), nil
}

// WorkflowOption configures a Workflow at construction time.
type WorkflowOption func(*Workflow)

// WithClock sets the clock used to stamp tokens as they enter a place. It only
// matters for definitions with timed transitions; pass a fixed clock in tests to
// make token entry times — and therefore the Due API — deterministic. A nil
// clock is ignored.
func WithClock(now func() time.Time) WorkflowOption {
	return func(w *Workflow) {
		if now != nil {
			w.now = now
		}
	}
}

// NewWorkflowFromMarking creates a workflow instance whose starting state is the
// given marking. Use it when the initial state has multiple places or
// data-carrying (colored) tokens; NewWorkflow is the single-place shorthand.
//
// An EMPTY marking (zero marked places) is valid: a pure token-pool net —
// one whose places are all legitimately empty between batches — starts with
// nothing marked and fires nothing until tokens arrive. For an ordinary
// start-to-finish workflow an empty start is a dead instance; declare an
// initial place there.
//
// The marking is adopted (owned by the workflow), and every place it occupies
// must be defined in the workflow. When the definition has timed transitions,
// tokens without an entry time are stamped at construction (so a fresh marking
// starts its timers); tokens that already carry an entry time keep it, so a
// persisted marking's running timers are restored rather than reset.
func NewWorkflowFromMarking(name string, definition *Definition, initial Marking, opts ...WorkflowOption) (*Workflow, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidWorkflow)
	}
	if definition == nil {
		return nil, fmt.Errorf("%w: definition cannot be nil", ErrInvalidDefinition)
	}
	if initial == nil {
		return nil, fmt.Errorf("%w: initial marking cannot be nil", ErrInvalidMarking)
	}

	places := initial.Places()
	for _, p := range places {
		if !definition.Place(p) {
			return nil, fmt.Errorf("%w: initial place %s is not defined in the workflow", ErrInvalidPlace, p)
		}
	}

	return newWorkflow(name, definition, initial, opts...), nil
}

// newWorkflow builds a Workflow from a marking, which is the single source of
// truth for the initial state: the initial places are derived from it rather than
// passed separately (so the two can never drift apart).
//
// When the definition has timed transitions, the initial marking's tokens are
// stamped with the workflow clock so the first timeout has a reference point.
func newWorkflow(name string, definition *Definition, marking Marking, opts ...WorkflowOption) *Workflow {
	w := &Workflow{
		name:          name,
		definition:    definition,
		initialPlaces: marking.Places(),
		marking:       marking,
		context:       make(map[string]any),
		manager:       nil,
		now:           time.Now,
	}
	for _, opt := range opts {
		opt(w)
	}
	// When the definition has timed transitions, stamp any initial tokens that
	// lack an entry time so the first timeout has a reference point; tokens that
	// already carry a stamp (a persisted marking being adopted) keep it.
	if definitionHasTimers(definition) {
		stampMarking(w.marking, w.now())
	}
	return w
}

// Name returns the workflow name
func (w *Workflow) Name() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.name
}

// AddEventListener adds an event listener for a specific event type
// It returns a handle that can be used to remove the listener later
func (w *Workflow) AddEventListener(eventType EventType, listener EventListener) *ListenerHandle {
	return w.listeners.add(eventType, listener, w)
}

// AddObserver adds a non-blocking observer for a specific event type on this
// instance: it cannot error, its panics are recovered, and it is the only
// listener kind that receives EventGuardRejected. Instrumentation belongs
// here (see ObserverFunc). It returns a handle for RemoveListener.
func (w *Workflow) AddObserver(eventType EventType, observer ObserverFunc) *ListenerHandle {
	return w.listeners.add(eventType, observer, w)
}

// AddGuardEventListener adds a guard event listener
// It returns a handle that can be used to remove the listener later
func (w *Workflow) AddGuardEventListener(listener GuardEventListener) *ListenerHandle {
	return w.listeners.add(EventGuard, listener, w)
}

// RemoveListener removes a listener using its handle
// This is the recommended way to remove listeners as it's reliable and efficient
func (w *Workflow) RemoveListener(handle *ListenerHandle) {
	if handle == nil || handle.owner != w {
		return
	}
	w.listeners.remove(handle)
}

// ListenerCount returns the number of listeners registered on this instance for
// eventType (definition- and manager-level listeners are not counted).
func (w *Workflow) ListenerCount(eventType EventType) int {
	return w.listeners.count(eventType)
}

// SetContext sets a value in the workflow context
func (w *Workflow) SetContext(key string, value any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.context[key] = value
}

// Context returns the value for the given key from the workflow context
func (w *Workflow) Context(key string) (any, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	value, ok := w.context[key]
	return value, ok
}

// AllContext returns a copy of all context values
func (w *Workflow) AllContext() map[string]any {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return maps.Clone(w.context)
}

// snapshotState returns a deep copy of the marking and a copy of the context
// map, both taken under the read lock, so persistence never marshals live
// state that a concurrent transition or SetContext could mutate mid-encode.
func (w *Workflow) snapshotState() (Marking, map[string]any) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return cloneMarking(w.marking), maps.Clone(w.context)
}

// SetManager sets the manager pointer for this workflow
func (w *Workflow) SetManager(m *Manager) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manager = m
}

// fireEvent fires listeners from definition, manager, and instance (in that
// order). Listener slices are snapshotted by the concurrency-safe listenerSet,
// and no workflow lock is held while calling user listeners, so listeners may
// re-enter the workflow and may be added/removed concurrently.
func (w *Workflow) fireEvent(event Event) error {
	eventType := event.Type()

	w.mu.RLock()
	definition := w.definition
	manager := w.manager
	w.mu.RUnlock()

	// 1. Definition listeners
	if definition != nil {
		if err := dispatchListeners(definition.listeners.snapshot(eventType), event); err != nil {
			return err
		}
	}
	// 2. Manager listeners
	if manager != nil {
		if err := dispatchListeners(manager.listeners.snapshot(eventType), event); err != nil {
			return err
		}
	}
	// 3. Instance listeners
	return dispatchListeners(w.listeners.snapshot(eventType), event)
}

// Can check if transition to target places is possible
func (w *Workflow) Can(to []Place) error {
	return w.CanWithContext(context.Background(), to)
}

// CanTransition checks if a specific transition by name is possible
func (w *Workflow) CanTransition(transitionName string) error {
	return w.CanTransitionWithContext(context.Background(), transitionName)
}

// CanTransitionWithContext checks if a specific transition by name is possible with a context
func (w *Workflow) CanTransitionWithContext(ctx context.Context, transitionName string) error {
	// Find the transition by name
	w.mu.RLock()
	definition := w.definition
	w.mu.RUnlock()

	var targetTransition *Transition
	for i := range definition.Transitions {
		if definition.Transitions[i].Name() == transitionName {
			targetTransition = &definition.Transitions[i]
			break
		}
	}

	if targetTransition == nil {
		return fmt.Errorf("%w: %s", ErrTransitionNotFound, transitionName)
	}

	// Check if transition is enabled: all 'from' places for the default
	// AND-join, or any one of them for an OR-input (FromAny) transition —
	// consumeFrom is the input set this firing will actually consume.
	currentPlaces := w.CurrentPlaces()
	consumeFrom, enabled := targetTransition.consumeSet(func(p Place) bool {
		return slices.Contains(currentPlaces, p)
	})
	if !enabled {
		return ErrNotEnabled
	}

	// Dynamic-cardinality joins: the marked-place check above is structural, so a
	// transition that waits for a runtime-resolved number of tokens is only
	// really enabled once its requirements are satisfied too.
	if err := w.requirementsMet(targetTransition); err != nil {
		return err
	}

	// Validate guard constraints
	tokens := w.coloredTokensAt(consumeFrom)
	event := NewGuardEvent(ctx, targetTransition, currentPlaces, targetTransition.To(), tokens, w)
	if err := targetTransition.validate(event); err != nil {
		if errors.Is(err, ErrGuardRejected) {
			w.notifyGuardRejected(ctx, targetTransition, consumeFrom, tokens)
		}
		return err
	}

	// Fire guard event listeners
	if err := w.fireEvent(event); err != nil {
		return err
	}
	if event.IsBlocking() {
		w.notifyGuardRejected(ctx, targetTransition, consumeFrom, tokens)
		return ErrGuardRejected
	}

	return nil
}

// notifyGuardRejected emits the observability-only EventGuardRejected. It is
// dispatched to observers exclusively (see dispatchListeners), so it can
// never error and never adds a failure mode to the rejection path.
func (w *Workflow) notifyGuardRejected(ctx context.Context, t *Transition, from []Place, tokens []Token) {
	_ = w.fireEvent(NewEvent(ctx, EventGuardRejected, t, from, t.To(), tokens, w))
}

// CanWithContext checks if transition to target places is possible with a context
func (w *Workflow) CanWithContext(ctx context.Context, to []Place) error {
	w.mu.RLock()
	definition := w.definition
	w.mu.RUnlock()
	// Check if transition is valid
	if len(to) == 0 {
		return ErrInvalidTransition
	}

	// Validate that all target places exist in workflow places
	for _, place := range to {
		if !definition.Place(place) {
			return ErrInvalidPlace
		}
	}

	// Get enabled transitions
	enabled, err := w.EnabledTransitions()
	if err != nil {
		return err
	}

	// Check if any enabled transition leads to the target places
	for _, t := range enabled {
		if len(t.To()) == len(to) {
			matches := true
			for i := range t.To() {
				if t.To()[i] != to[i] {
					matches = false
					break
				}
			}
			if matches {
				// Create guard event for validation
				event := NewGuardEvent(ctx, &t, w.marking.Places(), to, w.coloredTokensAt(t.From()), w)

				// First, validate transition constraints
				if err = t.validate(event); err != nil {
					continue
				}

				// Then, fire guard event listeners
				if err = w.fireEvent(event); err != nil {
					continue
				}
				if event.IsBlocking() {
					err = ErrGuardRejected
					continue
				}
				return nil
			}
		}
	}

	if err != nil {
		return err
	}

	return ErrNotEnabled
}

// Apply applies a transition to the workflow
func (w *Workflow) Apply(targetPlaces []Place) error {
	return w.ApplyWithContext(context.Background(), targetPlaces)
}

// ApplyTransition applies a specific transition by name
func (w *Workflow) ApplyTransition(transitionName string) error {
	return w.ApplyTransitionWithContext(context.Background(), transitionName)
}

// ApplyTransitionWithContext applies a specific transition by name with a context
func (w *Workflow) ApplyTransitionWithContext(ctx context.Context, transitionName string) error {
	// Find the transition by name
	w.mu.RLock()
	definition := w.definition
	w.mu.RUnlock()

	var targetTransition *Transition
	for i := range definition.Transitions {
		if definition.Transitions[i].Name() == transitionName {
			targetTransition = &definition.Transitions[i]
			break
		}
	}

	if targetTransition == nil {
		return fmt.Errorf("%w: %s", ErrTransitionNotFound, transitionName)
	}

	// Check if transition is enabled: all 'from' places for the default
	// AND-join, or any one of them for an OR-input (FromAny) transition —
	// consumeFrom is the input set this firing will actually consume.
	currentPlaces := w.CurrentPlaces()
	consumeFrom, enabled := targetTransition.consumeSet(func(p Place) bool {
		return slices.Contains(currentPlaces, p)
	})
	if !enabled {
		return ErrNotEnabled
	}

	// Dynamic-cardinality joins (see CanTransitionWithContext). This is the
	// cheap pre-lock answer; it is re-resolved under the write lock below, which
	// is also where the tokens to consume are selected.
	if err := w.requirementsMet(targetTransition); err != nil {
		return err
	}

	// Validate guard constraints
	guardTokens := w.coloredTokensAt(consumeFrom)
	event := NewGuardEvent(ctx, targetTransition, currentPlaces, targetTransition.To(), guardTokens, w)
	if err := targetTransition.validate(event); err != nil {
		if errors.Is(err, ErrGuardRejected) {
			w.notifyGuardRejected(ctx, targetTransition, consumeFrom, guardTokens)
		}
		return err
	}

	// Fire guard event listeners
	if err := w.fireEvent(event); err != nil {
		return err
	}
	if event.IsBlocking() {
		w.notifyGuardRejected(ctx, targetTransition, consumeFrom, guardTokens)
		return ErrGuardRejected
	}

	// Apply the transition directly (don't use Apply which might find wrong transition)
	w.mu.Lock()
	defer w.mu.Unlock()

	from := consumeFrom
	to := targetTransition.To()

	// Fire before transition event (unlock before calling listeners). Gather the
	// colored tokens being moved now, while they are still at the input places, so
	// both events can expose them.
	w.mu.Unlock()
	moved := w.coloredTokensAt(from)
	beforeEvent := NewEvent(ctx, EventBeforeTransition, targetTransition, from, to, moved, w)
	if err := w.fireEvent(beforeEvent); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	// Re-verify enablement under the write lock: the lock was released to run
	// guards and before-listeners, and a concurrent firing may have consumed the
	// input places in the meantime. Without this, two racing calls could both
	// pass the earlier check and both move (double-firing the boolean case, or
	// producing a phantom uncolored token in the colored case). For an
	// OR-input transition the consumed input is re-resolved here — the place
	// picked before the lock may have been drained meanwhile.
	from, enabled = targetTransition.consumeSet(w.marking.HasPlace)
	if !enabled {
		return ErrNotEnabled
	}

	// Re-resolve the requirements under the write lock and, with them, exactly
	// which tokens this firing consumes. The pre-lock answer is not good enough:
	// a concurrent approval may have landed in the meantime, which would make a
	// stale index set consume the wrong tokens.
	picked, err := w.selectRequirementsLocked(targetTransition)
	if err != nil {
		return err
	}

	// Move tokens from the input places to the output places (clearing any
	// reset places), preserving colored token data and leaving unrelated
	// places untouched. The consumed colored tokens replace the pre-lock
	// snapshot, which may be stale — the after-event must report the tokens
	// actually consumed.
	moved = w.moveMarking(from, to, targetTransition.Resets(), picked)
	// Log the firing while still holding the lock, before the after-event runs
	// listeners: a listener that errors aborts the caller's function, so
	// Execute never reaches the drain and the log is discarded with the attempt.
	w.recordFired(targetTransition.Name(), from, w.currentPlacesLocked())

	// Fire after transition event (unlock before calling listeners)
	w.mu.Unlock()
	afterEvent := NewEvent(ctx, EventAfterTransition, targetTransition, from, to, moved, w)
	if err := w.fireEvent(afterEvent); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	return nil
}

// ApplyWithContext applies a transition to the workflow with a context
func (w *Workflow) ApplyWithContext(ctx context.Context, targetPlaces []Place) error {
	// Validate target places first (before locking)
	for _, place := range targetPlaces {
		if !w.definition.Place(place) {
			return ErrInvalidPlace
		}
	}

	// Check if the transition is allowed (before locking)
	if err := w.CanWithContext(ctx, targetPlaces); err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Find the transition that leads to these places
	var from []Place
	var transition *Transition
	currentPlaces := w.marking.Places()

	// Check each transition
	for _, t := range w.definition.Transitions {
		// Enabled: all inputs marked (AND-join) or any one (OR-input).
		consumed, enabled := t.consumeSet(func(p Place) bool {
			return slices.Contains(currentPlaces, p)
		})

		// Check if all 'to' places match
		if enabled && len(t.To()) == len(targetPlaces) {
			matches := true
			for i := range t.To() {
				if t.To()[i] != targetPlaces[i] {
					matches = false
					break
				}
			}
			if matches {
				// A transition whose dynamic-cardinality join is not satisfied
				// is not enabled; keep looking for a sibling that is.
				if _, rerr := w.selectRequirementsLocked(&t); rerr != nil {
					if errors.Is(rerr, ErrNotEnabled) {
						continue
					}
					return rerr
				}
				from = consumed
				transition = &t
				break
			}
		}
	}

	if transition == nil {
		return ErrInvalidTransition
	}

	// Fire before transition event (unlock before calling listeners). Gather the
	// colored tokens being moved now, while they are still at the input places.
	w.mu.Unlock()
	moved := w.coloredTokensAt(from)
	event := NewEvent(ctx, EventBeforeTransition, transition, from, targetPlaces, moved, w)
	if err := w.fireEvent(event); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	// Re-verify enablement under the write lock (see ApplyTransitionWithContext):
	// a concurrent firing may have consumed the input places while the lock was
	// released for the before-listeners. The OR-input consume set and the
	// dynamic-cardinality selection are re-resolved for the same reason.
	var enabled bool
	from, enabled = transition.consumeSet(w.marking.HasPlace)
	if !enabled {
		return ErrNotEnabled
	}
	picked, err := w.selectRequirementsLocked(transition)
	if err != nil {
		return err
	}

	// Move tokens from the input places to the output places (clearing any
	// reset places), preserving colored token data and leaving unrelated
	// places untouched. The consumed colored tokens replace the pre-lock
	// snapshot, which may be stale.
	moved = w.moveMarking(from, targetPlaces, transition.Resets(), picked)
	w.recordFired(transition.Name(), from, w.currentPlacesLocked())

	// Fire after transition event (unlock before calling listeners)
	w.mu.Unlock()
	event = NewEvent(ctx, EventAfterTransition, transition, from, targetPlaces, moved, w)
	if err := w.fireEvent(event); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	return nil
}

// ApplyAny fires the first of the named transitions that the current state
// allows, returning the name of the one that fired. It is the free-choice /
// XOR-split resolver: declare guard-routed alternatives out of a place
// (e.g. auto-approve vs. full review, split by an amount guard) and let one
// call fire whichever the state enables.
//
// A candidate that is not enabled (ErrNotEnabled) or whose guard rejects it
// (ErrGuardRejected) is skipped and the next is tried; any other error —
// including ErrTransitionNotFound for a name that does not exist — aborts
// immediately. When no candidate fires, the last blocking error is returned
// so the caller can distinguish "nothing was enabled" from "a guard said no".
//
// Note the check-then-fire race inherent to trying candidates in order:
// under concurrent writers a candidate that was skipped may become enabled
// again by the time the call returns. Each individual firing is atomic; use
// Manager.Execute for load-fire-save cycles under optimistic concurrency.
func (w *Workflow) ApplyAny(ctx context.Context, names ...string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("%w: ApplyAny requires at least one transition name", ErrInvalidTransition)
	}
	var lastErr error
	for _, name := range names {
		err := w.ApplyTransitionWithContext(ctx, name)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, ErrTransitionNotAllowed) {
			return "", err
		}
		lastErr = err
	}
	return "", fmt.Errorf("no transition of %v fired: %w", names, lastErr)
}

// EnabledTransitions returns all transitions that can be applied in the current place
func (w *Workflow) EnabledTransitions() ([]Transition, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var enabled []Transition
	currentPlaces := w.marking.Places()

	// Check each transition: all inputs marked (AND-join) or any one
	// (OR-input / FromAny), then any dynamic-cardinality join over those
	// inputs. An unsatisfied requirement just means "not enabled yet"; a
	// requirement that cannot be evaluated at all is a definition fault and is
	// reported rather than silently hiding the transition.
	for _, trans := range w.definition.Transitions {
		if _, ok := trans.consumeSet(func(p Place) bool {
			return slices.Contains(currentPlaces, p)
		}); !ok {
			continue
		}
		if _, err := w.selectRequirementsLocked(&trans); err != nil {
			if errors.Is(err, ErrNotEnabled) {
				continue
			}
			return nil, err
		}
		enabled = append(enabled, trans)
	}
	return enabled, nil
}

// CurrentPlaces returns the current places of the workflow
func (w *Workflow) CurrentPlaces() []Place {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.marking.Places()
}

// currentPlacesLocked is CurrentPlaces for callers that already hold w.mu. The
// lock is a non-reentrant sync.RWMutex, so a firing path — which holds the
// write lock across the marking move — must use this, never CurrentPlaces.
func (w *Workflow) currentPlacesLocked() []Place {
	return w.marking.Places()
}

// Definition returns the workflow definition
func (w *Workflow) Definition() *Definition {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.definition
}

// Marking returns the current marking of the workflow
func (w *Workflow) Marking() Marking {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.marking
}

// SetMarking sets the workflow marking
func (w *Workflow) SetMarking(marking Marking) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if marking == nil {
		return fmt.Errorf("%w: marking cannot be nil", ErrInvalidMarking)
	}
	w.marking = marking
	return nil
}

// InitialPlace returns the workflow's first initial place. When the workflow
// started from a marking with multiple initial places it returns the first
// (sorted); use InitialPlaces for the full set.
func (w *Workflow) InitialPlace() Place {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.initialPlaces) == 0 {
		return ""
	}
	return w.initialPlaces[0]
}

// InitialPlaces returns a copy of the places the workflow's initial marking
// occupied.
func (w *Workflow) InitialPlaces() []Place {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]Place, len(w.initialPlaces))
	copy(out, w.initialPlaces)
	return out
}
