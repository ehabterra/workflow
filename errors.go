package workflow

import "errors"

// Sentinel errors returned by the workflow engine. Callers should test for these
// with errors.Is rather than comparing error strings, since most are returned
// wrapped with additional context (via fmt.Errorf("...: %w", err)).
var (
	// ErrTransitionNotAllowed is returned when a transition exists but its guards
	// or the current marking do not permit it to fire.
	ErrTransitionNotAllowed = errors.New("transition not allowed")

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
)
