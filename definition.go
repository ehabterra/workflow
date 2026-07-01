package workflow

import (
	"fmt"
	"sync/atomic"
)

// Definition represents a workflow definition with places and transitions
type Definition struct {
	Places      []Place
	Transitions []Transition

	// Default listeners for this workflow type
	Listeners map[EventType][]any

	// Handle tracking for reliable listener removal
	listenerHandles map[uint64]int // handle ID -> index in slice
	nextHandleID    uint64         // atomic counter for unique handle IDs
}

// NewDefinition creates a new workflow definition
func NewDefinition(places []Place, transitions []Transition) (*Definition, error) {
	// Create a map of valid places for quick lookup
	validPlaces := make(map[Place]bool)
	for _, place := range places {
		validPlaces[place] = true
	}

	// Validate all transitions
	for _, trans := range transitions {
		// Check 'from' places
		for _, place := range trans.From() {
			if !validPlaces[place] {
				return nil, fmt.Errorf("place '%s' in transition '%s' is not defined in workflow places", place, trans.Name())
			}
		}

		// Check 'to' places
		for _, place := range trans.To() {
			if !validPlaces[place] {
				return nil, fmt.Errorf("place '%s' in transition '%s' is not defined in workflow places", place, trans.Name())
			}
		}
	}

	return &Definition{
		Places:      places,
		Transitions: transitions,
	}, nil
}

// AllPlaces returns all places (places) in the definition
func (d *Definition) AllPlaces() []Place {
	places := make([]Place, len(d.Places))
	copy(places, d.Places)
	return places
}

// AllTransitions returns all transitions in the definition
func (d *Definition) AllTransitions() []Transition {
	transitions := make([]Transition, len(d.Transitions))
	copy(transitions, d.Transitions)
	return transitions
}

// Transition returns a transition by name
func (d *Definition) Transition(name string) *Transition {
	for _, t := range d.Transitions {
		if t.Name() == name {
			return &t
		}
	}
	return nil
}

// Place checks if a place exists in the definition
func (d *Definition) Place(place Place) bool {
	for _, p := range d.Places {
		if p == place {
			return true
		}
	}
	return false
}

// AddEventListener adds a default event listener for a specific event type
// It returns a handle that can be used to remove the listener later
func (d *Definition) AddEventListener(eventType EventType, listener EventListener) *ListenerHandle {
	if d.Listeners == nil {
		d.Listeners = make(map[EventType][]any)
	}
	if d.listenerHandles == nil {
		d.listenerHandles = make(map[uint64]int)
	}

	handleID := atomic.AddUint64(&d.nextHandleID, 1)
	index := len(d.Listeners[eventType])
	d.Listeners[eventType] = append(d.Listeners[eventType], listener)
	d.listenerHandles[handleID] = index

	return &ListenerHandle{
		id:        handleID,
		eventType: eventType,
		owner:     d,
	}
}

// AddGuardEventListener adds a default guard event listener
// It returns a handle that can be used to remove the listener later
func (d *Definition) AddGuardEventListener(listener GuardEventListener) *ListenerHandle {
	if d.Listeners == nil {
		d.Listeners = make(map[EventType][]any)
	}
	if d.listenerHandles == nil {
		d.listenerHandles = make(map[uint64]int)
	}

	handleID := atomic.AddUint64(&d.nextHandleID, 1)
	index := len(d.Listeners[EventGuard])
	d.Listeners[EventGuard] = append(d.Listeners[EventGuard], listener)
	d.listenerHandles[handleID] = index

	return &ListenerHandle{
		id:        handleID,
		eventType: EventGuard,
		owner:     d,
	}
}

// RemoveListener removes a listener using its handle
// This is the recommended way to remove listeners as it's reliable and efficient
func (d *Definition) RemoveListener(handle *ListenerHandle) {
	if d.Listeners == nil || d.listenerHandles == nil || handle == nil {
		return
	}

	// Verify the handle belongs to this definition
	if handle.owner != d {
		return
	}

	index, ok := d.listenerHandles[handle.id]
	if !ok {
		return // Handle not found
	}

	listeners := d.Listeners[handle.eventType]
	if index >= len(listeners) {
		return // Index out of bounds
	}

	// Remove from slice
	d.Listeners[handle.eventType] = append(listeners[:index], listeners[index+1:]...)

	// Update indices for handles after the removed one
	for id, idx := range d.listenerHandles {
		if idx > index {
			d.listenerHandles[id] = idx - 1
		}
	}

	delete(d.listenerHandles, handle.id)
}
