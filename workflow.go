package workflow

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
)

// Workflow represents a workflow instance
type Workflow struct {
	name         string
	definition   *Definition
	initialPlace Place
	marking      Marking
	listeners    map[EventType][]any
	context      map[string]any

	manager *Manager // pointer to manager, may be nil
	mu      sync.RWMutex

	// version is the optimistic-concurrency version last loaded from or saved to
	// a VersionedStorage backend. It is 0 for a workflow that has never been
	// persisted, and ignored by non-versioned backends.
	version int64

	// Handle tracking for reliable listener removal
	listenerHandles map[uint64]int // handle ID -> index in slice
	nextHandleID    uint64         // atomic counter for unique handle IDs
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

// Storage defines the interface for persisting workflow state.
// It is responsible for loading and saving the workflow's places (state)
// and its context data (custom fields).
//
// Every method takes a context.Context so callers can apply cancellation and
// deadlines; implementations are expected to honor it (e.g. by using the
// database/sql *Context methods).
type Storage interface {
	// LoadState loads the workflow's places and its context data for the given ID.
	LoadState(ctx context.Context, id string) (places []Place, context map[string]any, err error)

	// SaveState saves the workflow's places and its context data for the given ID.
	SaveState(ctx context.Context, id string, places []Place, context map[string]any) error

	// DeleteState removes the workflow state for the given ID.
	DeleteState(ctx context.Context, id string) error
}

// VersionedStorage is an optional interface a Storage backend may implement to
// support optimistic concurrency control. When the Manager is backed by a
// VersionedStorage it uses these methods so that two writers racing to save the
// same workflow cannot silently clobber each other: the second save fails with
// ErrConflict instead.
//
// Each workflow row carries a monotonically increasing version. A caller loads
// the current version with LoadVersionedState, and passes it back to
// SaveVersionedState; the save only succeeds if the stored version still matches.
type VersionedStorage interface {
	Storage

	// LoadVersionedState loads the workflow's places, context data, and current
	// version. A brand-new (never saved) workflow has version 0.
	LoadVersionedState(ctx context.Context, id string) (places []Place, context map[string]any, version int64, err error)

	// SaveVersionedState saves the workflow only if the stored version equals
	// expectedVersion, returning the new (incremented) version on success. Pass
	// expectedVersion 0 to create a new workflow. A mismatch — because another
	// writer saved first, or the row already exists — returns ErrConflict.
	SaveVersionedState(ctx context.Context, id string, places []Place, context map[string]any, expectedVersion int64) (newVersion int64, err error)
}

// NewWorkflow constructor
func NewWorkflow(name string, definition *Definition, initialPlace Place) (*Workflow, error) {
	if name == "" {
		return nil, fmt.Errorf("%w: name cannot be empty", ErrInvalidWorkflow)
	}

	if definition == nil {
		return nil, fmt.Errorf("%w: definition cannot be nil", ErrInvalidDefinition)
	}

	if !definition.Place(initialPlace) {
		return nil, fmt.Errorf("%w: initial place %s is not defined in the workflow", ErrInvalidPlace, initialPlace)
	}

	marking := NewMarking([]Place{initialPlace})

	return &Workflow{
		name:            name,
		definition:      definition,
		initialPlace:    initialPlace,
		marking:         marking,
		listeners:       make(map[EventType][]any),
		context:         make(map[string]any),
		manager:         nil,
		listenerHandles: make(map[uint64]int),
	}, nil
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
	w.mu.Lock()
	defer w.mu.Unlock()

	handleID := atomic.AddUint64(&w.nextHandleID, 1)
	index := len(w.listeners[eventType])
	w.listeners[eventType] = append(w.listeners[eventType], listener)
	w.listenerHandles[handleID] = index

	return &ListenerHandle{
		id:        handleID,
		eventType: eventType,
		owner:     w,
	}
}

// AddGuardEventListener adds a guard event listener
// It returns a handle that can be used to remove the listener later
func (w *Workflow) AddGuardEventListener(listener GuardEventListener) *ListenerHandle {
	w.mu.Lock()
	defer w.mu.Unlock()

	eventType := EventGuard
	handleID := atomic.AddUint64(&w.nextHandleID, 1)
	index := len(w.listeners[eventType])
	w.listeners[eventType] = append(w.listeners[eventType], listener)
	w.listenerHandles[handleID] = index

	return &ListenerHandle{
		id:        handleID,
		eventType: eventType,
		owner:     w,
	}
}

// RemoveListener removes a listener using its handle
// This is the recommended way to remove listeners as it's reliable and efficient
func (w *Workflow) RemoveListener(handle *ListenerHandle) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.listenerHandles == nil || handle == nil {
		return
	}

	// Verify the handle belongs to this workflow
	if handle.owner != w {
		return
	}

	index, ok := w.listenerHandles[handle.id]
	if !ok {
		return // Handle not found
	}

	listeners := w.listeners[handle.eventType]
	if index >= len(listeners) {
		return // Index out of bounds
	}

	// Remove from slice
	w.listeners[handle.eventType] = append(listeners[:index], listeners[index+1:]...)

	// Update indices for handles after the removed one
	for id, idx := range w.listenerHandles {
		if idx > index {
			w.listenerHandles[id] = idx - 1
		}
	}

	delete(w.listenerHandles, handle.id)
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
	result := make(map[string]any)
	for k, v := range w.context {
		result[k] = v
	}
	return result
}

// SetManager sets the manager pointer for this workflow
func (w *Workflow) SetManager(m *Manager) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manager = m
}

// fireEvent fires listeners from definition, manager, and instance (in that order)
func (w *Workflow) fireEvent(event Event) error {
	// Do not hold lock while calling user listeners to avoid deadlocks
	eventType := event.Type()

	// 1. Definition listeners
	if w.definition != nil && w.definition.Listeners != nil {
		for _, l := range w.definition.Listeners[eventType] {
			switch eventType {
			case EventGuard:
				if gl, ok := l.(GuardEventListener); ok {
					if err := gl(event.(*GuardEvent)); err != nil {
						return err
					}
				}
			default:
				if el, ok := l.(EventListener); ok {
					if err := el(event); err != nil {
						return err
					}
				}
			}
		}
	}
	// 2. Manager listeners
	w.mu.RLock()
	manager := w.manager
	w.mu.RUnlock()
	if manager != nil && manager.Listeners != nil {
		for _, l := range manager.Listeners[eventType] {
			switch eventType {
			case EventGuard:
				if gl, ok := l.(GuardEventListener); ok {
					if err := gl(event.(*GuardEvent)); err != nil {
						return err
					}
				}
			default:
				if el, ok := l.(EventListener); ok {
					if err := el(event); err != nil {
						return err
					}
				}
			}
		}
	}
	w.mu.RLock()
	listeners := w.listeners[eventType]
	w.mu.RUnlock()
	for _, l := range listeners {
		switch eventType {
		case EventGuard:
			if gl, ok := l.(GuardEventListener); ok {
				if err := gl(event.(*GuardEvent)); err != nil {
					return err
				}
			}
		default:
			if el, ok := l.(EventListener); ok {
				if err := el(event); err != nil {
					return err
				}
			}
		}
	}
	return nil
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

	// Check if transition is enabled (all 'from' places must be present)
	currentPlaces := w.CurrentPlaces()
	for _, fromPlace := range targetTransition.From() {
		if !slices.Contains(currentPlaces, fromPlace) {
			return ErrTransitionNotAllowed
		}
	}

	// Validate guard constraints
	event := NewGuardEvent(ctx, targetTransition, currentPlaces, targetTransition.To(), w)
	if err := targetTransition.validate(event); err != nil {
		return err
	}

	// Fire guard event listeners
	if err := w.fireEvent(event); err != nil {
		return err
	}
	if event.IsBlocking() {
		return ErrTransitionNotAllowed
	}

	return nil
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
				event := NewGuardEvent(ctx, &t, w.marking.Places(), to, w)

				// First, validate transition constraints
				if err = t.validate(event); err != nil {
					continue
				}

				// Then, fire guard event listeners
				if err = w.fireEvent(event); err != nil {
					continue
				}
				if event.IsBlocking() {
					err = ErrTransitionNotAllowed
					continue
				}
				return nil
			}
		}
	}

	if err != nil {
		return err
	}

	return ErrTransitionNotAllowed
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

	// Check if transition is enabled (all 'from' places must be present)
	currentPlaces := w.CurrentPlaces()
	for _, fromPlace := range targetTransition.From() {
		if !slices.Contains(currentPlaces, fromPlace) {
			return ErrTransitionNotAllowed
		}
	}

	// Validate guard constraints
	event := NewGuardEvent(ctx, targetTransition, currentPlaces, targetTransition.To(), w)
	if err := targetTransition.validate(event); err != nil {
		return err
	}

	// Fire guard event listeners
	if err := w.fireEvent(event); err != nil {
		return err
	}
	if event.IsBlocking() {
		return ErrTransitionNotAllowed
	}

	// Apply the transition directly (don't use Apply which might find wrong transition)
	w.mu.Lock()
	defer w.mu.Unlock()

	from := targetTransition.From()
	to := targetTransition.To()

	// Fire before transition event (unlock before calling listeners)
	w.mu.Unlock()
	beforeEvent := NewEvent(ctx, EventBeforeTransition, targetTransition, from, to, w)
	if err := w.fireEvent(beforeEvent); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	// Remove the 'from' places from marking
	currentPlaces = w.marking.Places()
	newPlaces := make([]Place, 0, len(currentPlaces))
	for _, place := range currentPlaces {
		if !slices.Contains(from, place) {
			newPlaces = append(newPlaces, place)
		}
	}

	// Add the target places to marking
	newPlaces = append(newPlaces, to...)
	w.marking.SetPlaces(newPlaces)

	// Fire after transition event (unlock before calling listeners)
	w.mu.Unlock()
	afterEvent := NewEvent(ctx, EventAfterTransition, targetTransition, from, to, w)
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
		// Check if all 'from' places are in current places
		allFromPlacesPresent := true
		for _, fromPlace := range t.From() {
			if !slices.Contains(currentPlaces, fromPlace) {
				allFromPlacesPresent = false
				break
			}
		}

		// Check if all 'to' places match
		if allFromPlacesPresent && len(t.To()) == len(targetPlaces) {
			matches := true
			for i := range t.To() {
				if t.To()[i] != targetPlaces[i] {
					matches = false
					break
				}
			}
			if matches {
				from = t.From()
				transition = &t
				break
			}
		}
	}

	if transition == nil {
		return ErrInvalidTransition
	}

	// Fire before transition event (unlock before calling listeners)
	w.mu.Unlock()
	event := NewEvent(ctx, EventBeforeTransition, transition, from, targetPlaces, w)
	if err := w.fireEvent(event); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	// Remove the 'from' places from marking
	newPlaces := make([]Place, 0, len(currentPlaces))
	for _, place := range currentPlaces {
		if !slices.Contains(from, place) {
			newPlaces = append(newPlaces, place)
		}
	}

	// Add the target places to marking
	newPlaces = append(newPlaces, targetPlaces...)
	w.marking.SetPlaces(newPlaces)

	// Fire after transition event (unlock before calling listeners)
	w.mu.Unlock()
	event = NewEvent(ctx, EventAfterTransition, transition, from, targetPlaces, w)
	if err := w.fireEvent(event); err != nil {
		w.mu.Lock()
		return err
	}
	w.mu.Lock()

	return nil
}

// EnabledTransitions returns all transitions that can be applied in the current place
func (w *Workflow) EnabledTransitions() ([]Transition, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	var enabled []Transition
	currentPlaces := w.marking.Places()

	// Check each transition
	for _, trans := range w.definition.Transitions {
		// Check if all 'from' places are in current places
		allFromPlacesPresent := true
		for _, fromPlace := range trans.From() {
			found := slices.Contains(currentPlaces, fromPlace)
			if !found {
				allFromPlacesPresent = false
				break
			}
		}

		if allFromPlacesPresent {
			enabled = append(enabled, trans)
		}
	}
	return enabled, nil
}

// CurrentPlaces returns the current places of the workflow
func (w *Workflow) CurrentPlaces() []Place {
	w.mu.RLock()
	defer w.mu.RUnlock()
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

// InitialPlace returns the initial place of the workflow
func (w *Workflow) InitialPlace() Place {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.initialPlace
}
