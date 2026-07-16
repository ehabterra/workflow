// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

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

	// placeMeta holds optional per-place metadata (places are bare strings and
	// cannot carry their own, unlike transitions). It is cosmetic — used for
	// diagram grouping (diagram_group) — and deliberately excluded from
	// Fingerprint, so regrouping a diagram never invalidates persisted state.
	placeMeta map[Place]map[string]any
}

// SetPlaceMetadata attaches metadata to a place. Places are bare strings, so
// this side-table is how a place carries diagram hints (e.g. diagram_group).
// It is cosmetic and not part of the definition's Fingerprint.
func (d *Definition) SetPlaceMetadata(p Place, meta map[string]any) {
	if d.placeMeta == nil {
		d.placeMeta = make(map[Place]map[string]any)
	}
	d.placeMeta[p] = meta
}

// PlaceMetadata returns the metadata value stored for a place under key,
// mirroring Transition.Metadata. The second result reports whether it was set.
func (d *Definition) PlaceMetadata(p Place, key string) (any, bool) {
	meta, ok := d.placeMeta[p]
	if !ok {
		return nil, false
	}
	v, ok := meta[key]
	return v, ok
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

		// Check reset places (cancellation regions)
		for _, place := range trans.Resets() {
			if !validPlaces[place] {
				return nil, fmt.Errorf("reset place '%s' in transition '%s' is not defined in workflow places", place, trans.Name())
			}
		}
	}

	return &Definition{
		Places:      places,
		Transitions: transitions,
	}, nil
}

// Fingerprint returns a stable SHA-256 hash of the definition's structure: its
// places and, for every transition, the name, input places, output places,
// guard expression string (as stored in the "guard" metadata), reset places,
// and input mode (AND-join vs OR-input). It is order-
// independent — places and transitions are canonically sorted first — so two
// definitions built in different orders but describing the same net share a
// fingerprint. Every component is length-prefixed before hashing, so no choice
// of place or transition name (including separator-looking characters) can make
// two structurally different definitions collide.
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

	// Serialize each transition into an unambiguous record (length-prefixed
	// fields), then sort the records for order independence.
	transitions := make([]string, len(d.Transitions))
	for i := range d.Transitions {
		transitions[i] = transitionRecord(&d.Transitions[i])
	}
	slices.Sort(transitions)

	h := sha256.New()
	var buf strings.Builder
	writeLenPrefixedList(&buf, places)
	writeLenPrefixedList(&buf, transitions)
	h.Write([]byte(buf.String()))
	return hex.EncodeToString(h.Sum(nil))
}

// transitionRecord serializes one transition's structural fields — name,
// sorted inputs/outputs, guard string, sorted resets, input mode — into an
// unambiguous length-prefixed record. Fingerprint hashes the whole sorted set
// of records; the definition shape (see diff.go) hashes each record
// individually so a later fingerprint mismatch can be diffed per transition.
func transitionRecord(t *Transition) string {
	from := placeStrings(t.From())
	to := placeStrings(t.To())
	slices.Sort(from)
	slices.Sort(to)
	guard := ""
	if g, ok := t.Metadata("guard"); ok {
		guard, _ = g.(string)
	}
	resets := placeStrings(t.Resets())
	slices.Sort(resets)
	inputMode := "all"
	if t.FromAny() {
		inputMode = "any"
	}
	var rec strings.Builder
	writeLenPrefixed(&rec, t.Name())
	writeLenPrefixedList(&rec, from)
	writeLenPrefixedList(&rec, to)
	writeLenPrefixed(&rec, guard)
	writeLenPrefixedList(&rec, resets)
	writeLenPrefixed(&rec, inputMode)
	return rec.String()
}

// writeLenPrefixed writes s preceded by its byte length ("3:abc"), making the
// concatenation of several values unambiguous regardless of their content.
func writeLenPrefixed(b *strings.Builder, s string) {
	fmt.Fprintf(b, "%d:", len(s))
	b.WriteString(s)
}

// writeLenPrefixedList writes the element count followed by each element
// length-prefixed, so lists of different shapes can never serialize equally.
func writeLenPrefixedList(b *strings.Builder, items []string) {
	fmt.Fprintf(b, "#%d;", len(items))
	for _, s := range items {
		writeLenPrefixed(b, s)
	}
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

// Transition returns a pointer to the transition with the given name, or nil if
// none matches. It points into the definition's own slice, so mutations through
// it (e.g. SetTimeoutAfter, SetMetadata) actually apply to the definition.
func (d *Definition) Transition(name string) *Transition {
	for i := range d.Transitions {
		if d.Transitions[i].Name() == name {
			return &d.Transitions[i]
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
