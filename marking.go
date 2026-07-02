package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// Marking represents the current state of a workflow as a Colored Petri Net
// marking: a mapping from each place to the multiset of tokens it holds.
//
// The engine treats a boolean/elementary net as the trivial case of this model —
// a place is "present" when it holds at least one token, and a plain workflow
// simply uses uncolored tokens (no ID, no data). You only ever touch the token
// methods when you actually need data-carrying tokens; everything else is served
// by the presence view (Places/HasPlace/AddPlace/RemovePlace).
type Marking interface {
	// --- Presence view (boolean/elementary semantics) ---

	// Places returns the places that currently hold at least one token.
	Places() []Place
	// SetPlaces resets the marking to a single uncolored token at each place.
	SetPlaces(places []Place)
	// HasPlace reports whether place holds at least one token.
	HasPlace(place Place) bool
	// AddPlace ensures place holds at least one token (adds an uncolored token
	// if it is currently empty).
	AddPlace(place Place) error
	// RemovePlace removes all tokens from place.
	RemovePlace(place Place) error

	// --- Token view (colored semantics) ---

	// TokensAt returns a copy of the tokens currently in place.
	TokensAt(place Place) []Token
	// TokenCount returns the number of tokens in place.
	TokenCount(place Place) int
	// AllTokens returns a copy of every place's tokens.
	AllTokens() map[Place][]Token
	// AddToken adds a token to place.
	AddToken(place Place, token Token)
	// RemoveToken removes the token with the given ID from place, returning
	// ErrTokenNotFound if it is not there.
	RemoveToken(place Place, id TokenID) error
	// HasToken reports whether a token with the given ID is in place.
	HasToken(place Place, id TokenID) bool
}

// marking is the single, unified Marking implementation. Uncolored tokens (empty
// ID, no data) represent boolean presence and cost nothing to generate; colored
// tokens carry an ID and data. The representation is adaptive on the wire: a
// marking with only single uncolored tokens serializes to the compact place-array
// form (["draft","review"]), so simple workflows persist exactly as before, while
// markings with real tokens serialize to a place→tokens object.
type marking struct {
	tokens map[Place][]Token
}

// NewMarking creates a marking with a single uncolored token at each of the given
// places.
func NewMarking(places []Place) Marking {
	m := &marking{tokens: make(map[Place][]Token, len(places))}
	for _, p := range places {
		m.tokens[p] = []Token{{}}
	}
	return m
}

// --- Presence view ---

func (m *marking) Places() []Place {
	out := make([]Place, 0, len(m.tokens))
	for p, toks := range m.tokens {
		if len(toks) > 0 {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

func (m *marking) SetPlaces(places []Place) {
	m.tokens = make(map[Place][]Token, len(places))
	for _, p := range places {
		m.tokens[p] = []Token{{}}
	}
}

func (m *marking) HasPlace(place Place) bool {
	return len(m.tokens[place]) > 0
}

func (m *marking) AddPlace(place Place) error {
	if len(m.tokens[place]) == 0 {
		m.tokens[place] = []Token{{}}
	}
	return nil
}

func (m *marking) RemovePlace(place Place) error {
	if len(m.tokens[place]) == 0 {
		return fmt.Errorf("%w: %s", ErrPlaceNotFound, place)
	}
	delete(m.tokens, place)
	return nil
}

// --- Token view ---

func (m *marking) TokensAt(place Place) []Token {
	return copyTokens(m.tokens[place])
}

func (m *marking) TokenCount(place Place) int {
	return len(m.tokens[place])
}

func (m *marking) AllTokens() map[Place][]Token {
	out := make(map[Place][]Token, len(m.tokens))
	for p, toks := range m.tokens {
		out[p] = copyTokens(toks)
	}
	return out
}

func (m *marking) AddToken(place Place, token Token) {
	m.tokens[place] = append(m.tokens[place], token)
}

func (m *marking) RemoveToken(place Place, id TokenID) error {
	toks := m.tokens[place]
	for i, t := range toks {
		if t.ID() == id {
			m.tokens[place] = slices.Delete(toks, i, i+1)
			if len(m.tokens[place]) == 0 {
				delete(m.tokens, place)
			}
			return nil
		}
	}
	return fmt.Errorf("%w: %s in place %s", ErrTokenNotFound, id, place)
}

func (m *marking) HasToken(place Place, id TokenID) bool {
	for _, t := range m.tokens[place] {
		if t.ID() == id {
			return true
		}
	}
	return false
}

// --- JSON (adaptive) ---

// isSimple reports whether every place holds exactly one uncolored token, in
// which case the marking can serialize to the compact place-array form.
func (m *marking) isSimple() bool {
	for _, toks := range m.tokens {
		if len(toks) != 1 {
			return false
		}
		if toks[0].id != "" || len(toks[0].data) != 0 {
			return false
		}
	}
	return true
}

// MarshalJSON emits ["p1","p2"] when the marking is simple, otherwise a
// {"p1":[tokens...]} object.
func (m *marking) MarshalJSON() ([]byte, error) {
	if m.isSimple() {
		return json.Marshal(m.Places())
	}
	return json.Marshal(m.tokens)
}

// UnmarshalJSON accepts either the compact place-array form or the token-object
// form, so state written by older versions still loads.
func (m *marking) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var places []Place
		if err := json.Unmarshal(data, &places); err != nil {
			return err
		}
		m.SetPlaces(places)
		return nil
	}
	tokens := make(map[Place][]Token)
	if err := json.Unmarshal(data, &tokens); err != nil {
		return err
	}
	m.tokens = tokens
	return nil
}

// UnmarshalMarkingJSON unmarshals JSON data into a Marking. It accepts both the
// compact place-array form and the token-object form.
func UnmarshalMarkingJSON(data []byte) (Marking, error) {
	m := &marking{tokens: make(map[Place][]Token)}
	if err := m.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	return m, nil
}

func copyTokens(toks []Token) []Token {
	if len(toks) == 0 {
		return nil
	}
	out := make([]Token, len(toks))
	copy(out, toks)
	return out
}
