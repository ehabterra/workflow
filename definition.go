package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
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

// Fingerprint returns a stable SHA-256 hash of the definition's structure: its
// places and, for every transition, the name, input places, output places, and
// guard expression string (as stored in the "guard" metadata). It is order-
// independent — places and transitions are canonically sorted first — so two
// definitions built in different orders but describing the same net share a
// fingerprint.
//
// The Manager stamps this on each persisted instance and compares it on load to
// catch a definition changing under running instances (see ErrDefinitionMismatch).
// Note: only expression guards recorded in transition metadata are captured;
// programmatic Go constraints without a "guard" metadata string are not part of
// the hash.
func (d *Definition) Fingerprint() string {
	places := make([]string, len(d.Places))
	for i, p := range d.Places {
		places[i] = string(p)
	}
	slices.Sort(places)

	transitions := make([]string, len(d.Transitions))
	for i := range d.Transitions {
		t := &d.Transitions[i]
		from := placeStrings(t.From())
		to := placeStrings(t.To())
		slices.Sort(from)
		slices.Sort(to)
		guard := ""
		if g, ok := t.Metadata("guard"); ok {
			guard, _ = g.(string)
		}
		// Field and record separators are bytes that cannot appear in a place or
		// transition name so the joined form is unambiguous.
		transitions[i] = strings.Join([]string{
			t.Name(),
			strings.Join(from, ","),
			strings.Join(to, ","),
			guard,
		}, "\x1f")
	}
	slices.Sort(transitions)

	h := sha256.New()
	h.Write([]byte(strings.Join(places, ",")))
	h.Write([]byte("\x1e"))
	h.Write([]byte(strings.Join(transitions, "\x1e")))
	return hex.EncodeToString(h.Sum(nil))
}

func placeStrings(places []Place) []string {
	out := make([]string, len(places))
	for i, p := range places {
		out[i] = string(p)
	}
	return out
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
	return slices.Contains(d.Places, place)
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
