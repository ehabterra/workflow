// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import "time"

// This file implements the host-driven timer model (roadmap M4). The library
// *models* time — it records when tokens enter a place and computes when a timed
// transition is due — but it never owns a clock or a goroutine: a host decides
// when to evaluate deadlines and fire due transitions (cron, ticker, or a work
// queue). Every evaluation takes an explicit `now`, so the whole model is a pure
// function of the marking and the current time, and unit-testable without sleeping.

// definitionHasTimers reports whether any transition in def carries a timeout. It
// gates all enteredAt stamping so a timer-free workflow is unchanged on the wire.
func definitionHasTimers(def *Definition) bool {
	if def == nil {
		return false
	}
	for i := range def.Transitions {
		if _, ok := def.Transitions[i].TimeoutAfter(); ok {
			return true
		}
	}
	return false
}

// stampMarking stamps the entry time of tokens that do not yet have one, leaving
// already-stamped tokens untouched. It is used to give a fresh workflow's initial
// marking a reference point for its first timeout, while preserving the running
// timers of a persisted marking adopted via NewWorkflowFromMarking. Places whose
// tokens are all already stamped are left completely undisturbed (no churn).
func stampMarking(m Marking, at time.Time) {
	for p, toks := range m.AllTokens() {
		if len(toks) == 0 {
			continue
		}
		needsStamp := false
		for _, tok := range toks {
			if _, ok := tok.EnteredAt(); !ok {
				needsStamp = true
				break
			}
		}
		if !needsStamp {
			continue // every token already has an entry time; preserve it as-is.
		}
		_ = m.RemovePlace(p)
		for _, tok := range toks {
			if _, ok := tok.EnteredAt(); ok {
				m.AddToken(p, tok) // keep the existing stamp
			} else {
				m.AddToken(p, tok.withEnteredAt(at))
			}
		}
	}
}

// placeEntered returns the time place p became continuously occupied — the entry
// time of its oldest current token — and whether that is known. It is unknown
// (ok=false) when p holds no tokens (not enabled) or holds any token without an
// entry time (e.g. state written before timer support), so such a place never
// makes a transition spuriously due.
func placeEntered(m Marking, p Place) (time.Time, bool) {
	toks := m.TokensAt(p)
	if len(toks) == 0 {
		return time.Time{}, false
	}
	var earliest time.Time
	for i, tok := range toks {
		at, ok := tok.EnteredAt()
		if !ok {
			return time.Time{}, false
		}
		if i == 0 || at.Before(earliest) {
			earliest = at
		}
	}
	return earliest, true
}

// transitionDeadline returns the wall-clock time at which transition t becomes
// due, and whether a deadline is running. A deadline runs only when t is timed
// AND currently enabled (every input place is occupied by a stamped token); the
// deadline is the moment the transition became fully enabled (the latest input
// entry time) plus its timeout.
func transitionDeadline(m Marking, t *Transition) (time.Time, bool) {
	d, ok := t.TimeoutAfter()
	if !ok {
		return time.Time{}, false
	}
	if t.fromAny {
		// OR-input: due as soon as ANY marked input's token has waited long
		// enough — the earliest deadline among the marked inputs.
		var deadline time.Time
		found := false
		for _, p := range t.from {
			at, ok := placeEntered(m, p)
			if !ok {
				continue
			}
			if d := at.Add(d); !found || d.Before(deadline) {
				deadline, found = d, true
			}
		}
		if !found {
			return time.Time{}, false
		}
		return deadline, true
	}
	var enabledAt time.Time
	for i, p := range t.from {
		at, ok := placeEntered(m, p)
		if !ok {
			return time.Time{}, false
		}
		if i == 0 || at.After(enabledAt) {
			enabledAt = at
		}
	}
	return enabledAt.Add(d), true
}

// dueTransitions returns the timed transitions whose deadline has elapsed by now.
func dueTransitions(def *Definition, m Marking, now time.Time) []Transition {
	if def == nil {
		return nil
	}
	var out []Transition
	for i := range def.Transitions {
		t := &def.Transitions[i]
		deadline, ok := transitionDeadline(m, t)
		if ok && !now.Before(deadline) {
			out = append(out, *t)
		}
	}
	return out
}

// nextDue returns the earliest deadline across all running timed transitions and
// whether any exists. The deadline may be in the past (a transition already due);
// a host compares it against its own clock to decide when to next evaluate.
func nextDue(def *Definition, m Marking) (time.Time, bool) {
	if def == nil {
		return time.Time{}, false
	}
	var earliest time.Time
	found := false
	for i := range def.Transitions {
		deadline, ok := transitionDeadline(m, &def.Transitions[i])
		if !ok {
			continue
		}
		if !found || deadline.Before(earliest) {
			earliest = deadline
			found = true
		}
	}
	return earliest, found
}

// Due returns the transitions whose timeout has elapsed as of now — those a host
// should fire to honor a deadline (e.g. "escalate if not approved in 3 days").
// A transition is included only if it is timed (SetTimeoutAfter) and currently
// enabled. Guards are not evaluated here; firing still honors them, so a due
// transition with a blocking guard will be skipped by Manager.FireDue.
//
// now is always explicit so the result is a pure function of the marking and the
// given time — deterministic and testable without a real clock.
func (w *Workflow) Due(now time.Time) []Transition {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return dueTransitions(w.definition, w.marking, now)
}

// NextDue returns the earliest time at which some timed transition becomes due,
// and false if no timed transition is currently enabled. The time may be in the
// past if a deadline has already elapsed. Hosts use it to schedule the next
// evaluation instead of polling.
func (w *Workflow) NextDue() (time.Time, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return nextDue(w.definition, w.marking)
}
