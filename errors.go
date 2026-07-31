// Copyright (c) 2025 Ehab Terra
// SPDX-License-Identifier: MIT

package workflow

import "errors"

// Sentinel errors returned by the workflow engine. Callers should test for these
// with errors.Is rather than comparing error strings, since most are returned
// wrapped with additional context (via fmt.Errorf("...: %w", err)).
var (
	// ErrTransitionNotAllowed is returned when a transition exists but its guards
	// or the current marking do not permit it to fire.
	//
	// It is the general condition; the two concrete reasons — ErrNotEnabled and
	// ErrGuardRejected — both satisfy errors.Is(err, ErrTransitionNotAllowed), so
	// existing callers that test the general sentinel keep working while new
	// callers can distinguish the cause.
	ErrTransitionNotAllowed = errors.New("transition not allowed")

	// ErrNotEnabled is returned when a transition cannot fire because the current
	// marking does not enable it — one or more of its input places is unmarked.
	// This is the "try again later / already advanced" case: for example, a
	// webhook redelivery that re-fires a transition an earlier delivery already
	// took. It satisfies errors.Is(err, ErrTransitionNotAllowed).
	ErrNotEnabled = &blockedError{msg: "transition not enabled in current marking"}

	// ErrGuardRejected is returned when a transition is enabled by the marking but
	// a guard constraint or a blocking guard listener refused it — the "forbidden
	// / conditions not met" case (e.g. insufficient permissions, amount over a
	// limit). It satisfies errors.Is(err, ErrTransitionNotAllowed).
	ErrGuardRejected = &blockedError{msg: "transition rejected by guard"}

	// ErrTransitionNotFound is returned when no transition matches the requested
	// name or target places.
	ErrTransitionNotFound = errors.New("transition not found")

	// ErrInvalidPlace is returned for a place that is not defined in the workflow.
	ErrInvalidPlace = errors.New("invalid place")

	// ErrInvalidTransition is returned when a transition definition is invalid.
	ErrInvalidTransition = errors.New("invalid transition")

	// ErrPlaceNotFound is returned when a place is not present in the current marking.
	ErrPlaceNotFound = errors.New("place not found")

	// ErrWorkflowNotFound is returned when a workflow cannot be located in the
	// registry or storage.
	ErrWorkflowNotFound = errors.New("workflow not found")

	// ErrWorkflowExists is returned when adding a workflow whose name is already
	// registered.
	ErrWorkflowExists = errors.New("workflow already exists")

	// ErrInvalidWorkflow is returned when a workflow or its definition is nil or
	// otherwise malformed.
	ErrInvalidWorkflow = errors.New("invalid workflow")

	// ErrInvalidDefinition is returned when a workflow definition is nil or invalid.
	ErrInvalidDefinition = errors.New("invalid definition")

	// ErrInvalidMarking is returned when a marking is nil or invalid.
	ErrInvalidMarking = errors.New("invalid marking")

	// ErrInvalidExpression is returned when a guard expression is empty or fails to
	// compile.
	ErrInvalidExpression = errors.New("invalid expression")

	// ErrConflict is returned by a VersionedStorage when a save is rejected because
	// the stored version no longer matches the expected version — i.e. another writer
	// updated the workflow first (optimistic-concurrency conflict).
	ErrConflict = errors.New("version conflict")

	// ErrInvalidToken is returned when a token is malformed (for example it has an
	// empty ID).
	ErrInvalidToken = errors.New("invalid token")

	// ErrTokenNotFound is returned when a token cannot be found in a place.
	ErrTokenNotFound = errors.New("token not found")

	// ErrNoTransaction is returned when a transaction-scoped guard (see
	// NewTxExpressionConstraint) is evaluated with no firing transaction bound —
	// a bare Workflow.ApplyTransition, or a CanTransition probe outside Execute.
	// Such a guard exists precisely because a stale answer is wrong, so it
	// refuses to give one rather than silently reading outside the transaction.
	//
	// A backend that cannot open a scope is a different failure and is reported
	// differently: Manager.Execute rejects it with errors.ErrUnsupported before
	// anything fires, so the problem is named as a missing capability rather
	// than as a guard that happened to have no transaction.
	ErrNoTransaction = errors.New("no active transaction for a transaction-scoped guard")

	// ErrDefinitionMismatch is returned by the Manager when a persisted instance
	// was created against a different workflow definition than the one supplied to
	// load it (the stored definition fingerprint differs). It guards against
	// silently running an old marking against an incompatible definition. Supply a
	// migration handler (WithDefinitionMigration) to load such instances anyway.
	ErrDefinitionMismatch = errors.New("workflow definition mismatch")
)

// blockedError is a sentinel that also reports itself as ErrTransitionNotAllowed,
// so a specific reason (ErrNotEnabled / ErrGuardRejected) can be matched
// precisely while errors.Is(err, ErrTransitionNotAllowed) still holds.
type blockedError struct{ msg string }

func (e *blockedError) Error() string { return e.msg }

// Is reports a match for the concrete sentinel (handled by errors.Is via ==) or
// for the general ErrTransitionNotAllowed condition.
func (e *blockedError) Is(target error) bool { return target == ErrTransitionNotAllowed }
