package workflow

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Workflow represents a workflow instance
type Workflow struct {
	name         string
	definition   *Definition
	initialPlace Place
	marking      Marking
	listeners    map[EventType][]interface{}
	context      map[string]interface{}

	manager *Manager // pointer to manager, may be nil
	mu      sync.RWMutex
}

// Storage defines the interface for persisting workflow state.
// It is responsible for loading and saving the workflow's places (state)
// and its context data (custom fields).
type Storage interface {
	// LoadState loads the workflow's places and its context data for the given ID.
	LoadState(id string) (places []Place, context map[string]interface{}, err error)

	// SaveState saves the workflow's places and its context data for the given ID.
	SaveState(id string, places []Place, context map[string]interface{}) error

	// DeleteState removes the workflow state for the given ID.
	DeleteState(id string) error
}

// NewWorkflow constructor
func NewWorkflow(name string, definition *Definition, initialPlace Place) (*Workflow, error) {
	if name == "" {
		return nil, fmt.Errorf("workflow name cannot be empty")
	}

	if definition == nil {
		return nil, fmt.Errorf("workflow definition cannot be nil")
	}

	if !definition.Place(initialPlace) {
		return nil, fmt.Errorf("initial place %s is not defined in the workflow", initialPlace)
	}

	marking := NewMarking([]Place{initialPlace})

	return &Workflow{
		name:         name,
		definition:   definition,
		initialPlace: initialPlace,
		marking:      marking,
		listeners:    make(map[EventType][]interface{}),
		context:      make(map[string]interface{}),
		manager:      nil,
	}, nil
}

// Name returns the workflow name
func (w *Workflow) Name() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.name
}

// AddEventListener adds an event listener for a specific event type
func (w *Workflow) AddEventListener(eventType EventType, listener EventListener) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.listeners[eventType] = append(w.listeners[eventType], listener)
}

// AddGuardEventListener adds a guard event listener
func (w *Workflow) AddGuardEventListener(listener GuardEventListener) {
	w.mu.Lock()
	defer w.mu.Unlock()
	eventType := EventGuard
	w.listeners[eventType] = append(w.listeners[eventType], listener)
}

// RemoveEventListener removes an event listener
func (w *Workflow) RemoveEventListener(eventType EventType, listener interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	listeners := w.listeners[eventType]
	for i, l := range listeners {
		if &l == &listener {
			w.listeners[eventType] = append(listeners[:i], listeners[i+1:]...)
			break
		}
	}
}

// SetContext sets a value in the workflow context
func (w *Workflow) SetContext(key string, value interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.context[key] = value
}

// Context returns the value for the given key from the workflow context
func (w *Workflow) Context(key string) (interface{}, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	value, ok := w.context[key]
	return value, ok
}

// AllContext returns a copy of all context values
func (w *Workflow) AllContext() map[string]interface{} {
	w.mu.RLock()
	defer w.mu.RUnlock()
	result := make(map[string]interface{})
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
		return fmt.Errorf("transition %s not found", transitionName)
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
		return fmt.Errorf("transition %s not found", transitionName)
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
		return fmt.Errorf("marking cannot be nil")
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
