package workflow

import (
	"fmt"
)

// Definition represents a workflow definition with places and transitions
type Definition struct {
	Places      []Place
	Transitions []Transition

	// listeners holds the default listeners for every workflow instance built
	// from this definition. It is concurrency-safe: listeners may be added or
	// removed while instances fire transitions on other goroutines.
	listeners listenerSet
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
	return d.listeners.add(eventType, listener, d)
}

// AddGuardEventListener adds a default guard event listener
// It returns a handle that can be used to remove the listener later
func (d *Definition) AddGuardEventListener(listener GuardEventListener) *ListenerHandle {
	return d.listeners.add(EventGuard, listener, d)
}

// RemoveListener removes a listener using its handle
// This is the recommended way to remove listeners as it's reliable and efficient
func (d *Definition) RemoveListener(handle *ListenerHandle) {
	if handle == nil || handle.owner != d {
		return
	}
	d.listeners.remove(handle)
}

// ListenerCount returns the number of listeners registered for eventType.
func (d *Definition) ListenerCount(eventType EventType) int {
	return d.listeners.count(eventType)
}
