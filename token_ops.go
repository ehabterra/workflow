package workflow

import (
	"fmt"
	"time"
)

// Token operations on a Workflow.
//
// Every workflow's marking is token-capable: a plain workflow simply holds
// uncolored tokens (boolean presence), while these methods let you introduce and
// inspect data-carrying (colored) tokens for Colored Petri Net workflows such as
// batch processing. Using them does not put the workflow into a special "mode" —
// it just adds tokens with data.

// CreateToken creates a token carrying data at place and returns it. It fails
// with ErrInvalidPlace if place is not part of the definition.
func (w *Workflow) CreateToken(place Place, data TokenData) (Token, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.definition.Place(place) {
		return Token{}, fmt.Errorf("%w: %s", ErrInvalidPlace, place)
	}
	tok := NewToken(data)
	// Stamp the entry time when the definition has timed transitions, so a token
	// seeded directly into a timed place starts its deadline like one produced by
	// firing would. Without this the place would look unstamped and never be due.
	if definitionHasTimers(w.definition) {
		tok = tok.withEnteredAt(w.now())
	}
	w.marking.AddToken(place, tok)
	return tok, nil
}

// CreateTokens creates one token per entry in datas at place, returning them in
// order. It is a batch convenience over CreateToken.
func (w *Workflow) CreateTokens(place Place, datas []TokenData) ([]Token, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.definition.Place(place) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidPlace, place)
	}
	hasTimers := definitionHasTimers(w.definition)
	var enteredAt time.Time
	if hasTimers {
		enteredAt = w.now()
	}
	tokens := make([]Token, 0, len(datas))
	for _, d := range datas {
		tok := NewToken(d)
		if hasTimers {
			tok = tok.withEnteredAt(enteredAt)
		}
		w.marking.AddToken(place, tok)
		tokens = append(tokens, tok)
	}
	return tokens, nil
}

// GetTokens returns a copy of the tokens currently at place.
func (w *Workflow) GetTokens(place Place) []Token {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.marking.TokensAt(place)
}

// TokenCount returns the number of tokens at place.
func (w *Workflow) TokenCount(place Place) int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.marking.TokenCount(place)
}

// AllTokens returns a copy of every place's tokens.
func (w *Workflow) AllTokens() map[Place][]Token {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.marking.AllTokens()
}

// RemoveToken removes the token with the given ID from place, returning
// ErrTokenNotFound if it is not there.
func (w *Workflow) RemoveToken(place Place, id TokenID) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.marking.RemoveToken(place, id)
}

// ClearPlace removes every token from place — both colored tokens and the
// uncolored presence token a boolean workflow starts with. It is a no-op if the
// place is already empty. This is useful when seeding a Colored Petri Net place
// with an exact set of tokens (so the initial presence token does not linger).
func (w *Workflow) ClearPlace(place Place) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.marking.RemovePlace(place) // no-op error when already empty
}
