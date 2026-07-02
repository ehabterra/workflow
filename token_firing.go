package workflow

import (
	"context"
	"fmt"
	"slices"
)

// TokenPredicate reports whether a token should be selected. A nil predicate
// matches every token.
type TokenPredicate func(Token) bool

// isColored reports whether a token carries identity or data, as opposed to an
// uncolored presence token ({} — empty ID, no data) used for boolean workflows.
func isColored(t Token) bool { return t.ID() != "" || len(t.data) > 0 }

// moveMarking consumes the tokens at the from places and produces them at the to
// places, leaving every other place untouched. This is the token-aware
// replacement for a whole-marking reset: it preserves colored tokens (and their
// data) as they flow through a transition.
//
// When a single output place is involved, consumed colored tokens keep their
// identities (the common linear/batch case). For an AND-split (multiple outputs)
// each output receives a fresh copy of every consumed token. When no colored
// tokens are consumed it falls back to boolean presence (one uncolored token per
// output). Callers must hold w.mu.
func (w *Workflow) moveMarking(from, to []Place) {
	var carried []Token
	for _, f := range from {
		for _, tok := range w.marking.TokensAt(f) {
			if isColored(tok) {
				carried = append(carried, tok)
			}
		}
		_ = w.marking.RemovePlace(f) // removes all tokens at f; no-op error if empty
	}
	w.produce(to, carried)
}

// produce places carried tokens at each output place (see moveMarking). Callers
// must hold w.mu.
func (w *Workflow) produce(to []Place, carried []Token) {
	singleOutput := len(to) == 1
	for _, t := range to {
		if len(carried) == 0 {
			_ = w.marking.AddPlace(t) // boolean presence
			continue
		}
		for _, tok := range carried {
			if singleOutput {
				w.marking.AddToken(t, tok)
			} else {
				w.marking.AddToken(t, NewToken(tok.Data())) // copy with a fresh ID
			}
		}
	}
}

// SelectTokens returns the tokens at place that match pred (a nil pred returns
// all tokens). It is a read-only helper for choosing which token(s) to advance
// with ApplyTransitionForToken.
func (w *Workflow) SelectTokens(place Place, pred TokenPredicate) []Token {
	w.mu.RLock()
	defer w.mu.RUnlock()

	tokens := w.marking.TokensAt(place)
	if pred == nil {
		return tokens
	}
	out := make([]Token, 0, len(tokens))
	for _, tok := range tokens {
		if pred(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// ApplyTransitionForToken fires transitionName consuming exactly the token with
// tokenID from the transition's input place, producing it (data preserved) at the
// transition's output place(s). It is the Colored Petri Net way to advance one
// token out of a place that holds several — for example, processing a single
// order from a batch.
//
// The transition must have a single input place, and tokenID must currently be in
// it. Guards are validated exactly as for ApplyTransition.
func (w *Workflow) ApplyTransitionForToken(ctx context.Context, transitionName string, tokenID TokenID) error {
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

	from := targetTransition.From()
	to := targetTransition.To()
	if len(from) != 1 {
		return fmt.Errorf("%w: per-token firing requires a single input place, transition %s has %d",
			ErrInvalidTransition, transitionName, len(from))
	}
	inputPlace := from[0]

	// The token must be present in the input place.
	w.mu.RLock()
	hasToken := w.marking.HasToken(inputPlace, tokenID)
	currentPlaces := w.marking.Places()
	w.mu.RUnlock()
	if !hasToken {
		return fmt.Errorf("%w: %s in place %s", ErrTokenNotFound, tokenID, inputPlace)
	}
	for _, fromPlace := range from {
		if !slices.Contains(currentPlaces, fromPlace) {
			return ErrTransitionNotAllowed
		}
	}

	// Validate guard constraints and listeners (same as ApplyTransition).
	event := NewGuardEvent(ctx, targetTransition, currentPlaces, to, w)
	if err := targetTransition.validate(event); err != nil {
		return err
	}
	if err := w.fireEvent(event); err != nil {
		return err
	}
	if event.IsBlocking() {
		return ErrTransitionNotAllowed
	}

	// Fire before-transition listeners.
	beforeEvent := NewEvent(ctx, EventBeforeTransition, targetTransition, from, to, w)
	if err := w.fireEvent(beforeEvent); err != nil {
		return err
	}

	// Consume exactly the selected token and produce it at the outputs.
	w.mu.Lock()
	tok, ok := w.tokenAt(inputPlace, tokenID)
	if !ok {
		w.mu.Unlock()
		return fmt.Errorf("%w: %s in place %s", ErrTokenNotFound, tokenID, inputPlace)
	}
	if err := w.marking.RemoveToken(inputPlace, tokenID); err != nil {
		w.mu.Unlock()
		return err
	}
	w.produce(to, []Token{tok})
	w.mu.Unlock()

	// Fire after-transition listeners.
	afterEvent := NewEvent(ctx, EventAfterTransition, targetTransition, from, to, w)
	return w.fireEvent(afterEvent)
}

// tokenAt returns the token with id at place. Callers must hold w.mu.
func (w *Workflow) tokenAt(place Place, id TokenID) (Token, bool) {
	for _, tok := range w.marking.TokensAt(place) {
		if tok.ID() == id {
			return tok, true
		}
	}
	return Token{}, false
}
