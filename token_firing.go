// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// TokenPredicate reports whether a token should be selected. A nil predicate
// matches every token.
type TokenPredicate func(Token) bool

// isColored reports whether a token carries identity or data, as opposed to an
// uncolored presence token ({} — empty ID, no data) used for boolean workflows.
func isColored(t Token) bool { return t.ID() != "" || len(t.data) > 0 }

// coloredTokensAt returns the colored (data-carrying) tokens across the given
// places, used to populate events. It takes a read lock, so callers must NOT
// already hold w.mu.
func (w *Workflow) coloredTokensAt(places []Place) []Token {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.coloredTokensAtLocked(places)
}

// coloredTokensAtLocked is coloredTokensAt for callers that already hold w.mu
// (the RWMutex is not reentrant).
func (w *Workflow) coloredTokensAtLocked(places []Place) []Token {
	var out []Token
	for _, p := range places {
		for _, tok := range w.marking.TokensAt(p) {
			if isColored(tok) {
				out = append(out, tok)
			}
		}
	}
	return out
}

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
func (w *Workflow) moveMarking(from, to, resets []Place) {
	var carried []Token
	for _, f := range from {
		for _, tok := range w.marking.TokensAt(f) {
			if isColored(tok) {
				carried = append(carried, tok)
			}
		}
		_ = w.marking.RemovePlace(f) // removes all tokens at f; no-op error if empty
	}
	// Reset arcs (cancellation regions): empty every reset place after the
	// inputs are consumed and BEFORE the outputs are produced, so a place
	// that is both reset and an output keeps what this firing produces. Any
	// timer running on a cleared token dies with it — the due index is
	// recomputed from the marking on save.
	for _, p := range resets {
		_ = w.marking.RemovePlace(p)
	}
	w.produce(to, carried)
}

// produce places carried tokens at each output place (see moveMarking). When the
// definition has timed transitions, every produced token is stamped with the
// workflow clock so the Due API can measure how long it has waited. Callers must
// hold w.mu.
func (w *Workflow) produce(to []Place, carried []Token) {
	hasTimers := definitionHasTimers(w.definition)
	var enteredAt time.Time
	if hasTimers {
		enteredAt = w.now()
	}
	stamp := func(tok Token) Token {
		if hasTimers {
			return tok.withEnteredAt(enteredAt)
		}
		return tok
	}

	singleOutput := len(to) == 1
	for _, t := range to {
		if len(carried) == 0 {
			if hasTimers {
				// A stamped boolean-presence token so a timer can run on it — but
				// only when the place is currently unoccupied, mirroring AddPlace's
				// no-op-when-occupied semantics so firing stays idempotent for
				// boolean presence (an AND-join into an occupied place adds nothing).
				if !w.marking.HasPlace(t) {
					w.marking.AddToken(t, Token{}.withEnteredAt(enteredAt))
				}
			} else {
				_ = w.marking.AddPlace(t) // boolean presence (no-op if occupied)
			}
			continue
		}
		for _, tok := range carried {
			if singleOutput {
				w.marking.AddToken(t, stamp(tok))
			} else {
				w.marking.AddToken(t, stamp(NewToken(tok.Data()))) // copy with a fresh ID
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
//
// Concurrency: token consumption is atomic. The token's presence is re-checked
// under the write lock immediately before it is removed, so callers racing to
// advance the same token cannot double-consume it — the loser gets
// ErrTokenNotFound and no token is lost or duplicated. Guards, like the
// whole-marking Apply methods, are evaluated on a snapshot taken before the final
// lock (event listeners must run unlocked because they may re-enter the
// workflow); a guard that depends on marking state beyond the token's own
// presence may therefore observe a slightly stale view under concurrency.
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
	if !targetTransition.FromAny() && len(from) != 1 {
		return fmt.Errorf("%w: per-token firing requires a single input place, transition %s has %d",
			ErrInvalidTransition, transitionName, len(from))
	}

	// Resolve the input place: the declared one, or — for an OR-input
	// transition — whichever input currently holds the token. Snapshot the
	// token so guards and listeners can inspect it (per-token routing).
	w.mu.RLock()
	inputPlace := from[0]
	tok, hasToken := w.tokenAt(inputPlace, tokenID)
	if targetTransition.FromAny() {
		for _, p := range from {
			if t, ok := w.tokenAt(p, tokenID); ok {
				inputPlace, tok, hasToken = p, t, true
				break
			}
		}
	}
	currentPlaces := w.marking.Places()
	w.mu.RUnlock()
	if !hasToken {
		return fmt.Errorf("%w: %s in place %s", ErrTokenNotFound, tokenID, inputPlace)
	}
	if !slices.Contains(currentPlaces, inputPlace) {
		return ErrNotEnabled
	}
	eventTokens := []Token{tok}

	// Validate guard constraints and listeners (same as ApplyTransition). The
	// guard can route on the token's data (e.g. token.amount > 1000).
	event := NewGuardEvent(ctx, targetTransition, currentPlaces, to, eventTokens, w)
	if err := targetTransition.validate(event); err != nil {
		if errors.Is(err, ErrGuardRejected) {
			w.notifyGuardRejected(ctx, targetTransition, from, eventTokens)
		}
		return err
	}
	if err := w.fireEvent(event); err != nil {
		return err
	}
	if event.IsBlocking() {
		w.notifyGuardRejected(ctx, targetTransition, from, eventTokens)
		return ErrGuardRejected
	}

	// Fire before-transition listeners.
	beforeEvent := NewEvent(ctx, EventBeforeTransition, targetTransition, from, to, eventTokens, w)
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
	// Reset arcs clear WHOLE places, also in per-token firing — a reset on
	// the input place removes the sibling tokens that were not consumed.
	for _, p := range targetTransition.Resets() {
		_ = w.marking.RemovePlace(p)
	}
	w.produce(to, []Token{tok})
	w.recordFired(targetTransition.Name(), from, w.currentPlacesLocked())
	w.mu.Unlock()

	// Fire after-transition listeners.
	afterEvent := NewEvent(ctx, EventAfterTransition, targetTransition, from, to, eventTokens, w)
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
