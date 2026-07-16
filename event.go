package workflow

import (
	"context"
)

// EventType represents the type of workflow event
type EventType string

const (
	// EventBeforeTransition is fired before a transition is applied
	EventBeforeTransition EventType = "before_transition"
	// EventAfterTransition is fired after a transition is applied
	EventAfterTransition EventType = "after_transition"
	// EventGuard is fired to check if a transition is allowed
	EventGuard EventType = "guard"
	// EventGuardRejected is fired when a firing attempt is refused by a guard
	// (an expression constraint or a blocking guard listener). It is an
	// OBSERVABILITY-ONLY event: it is dispatched exclusively to observers
	// (AddObserver) — error-returning listeners registered for it are never
	// invoked — so instrumenting rejections can never add a failure mode to
	// the rejection path itself.
	EventGuardRejected EventType = "guard_rejected"
)

// Event defines the common interface for all event types
type Event interface {
	Type() EventType
	Transition() *Transition
	From() []Place
	To() []Place
	// Tokens returns the tokens involved in this firing: exactly one for per-token
	// firing (ApplyTransitionForToken), the consumed colored tokens for a
	// whole-marking firing, or empty for a purely boolean firing.
	Tokens() []Token
	Workflow() *Workflow
	Context() context.Context
}

// BaseEvent represents a workflow event
type BaseEvent struct {
	eventType  EventType
	transition *Transition
	from       []Place
	to         []Place
	tokens     []Token
	workflow   *Workflow

	ctx context.Context
}

// NewEvent creates a new BaseEvent instance. tokens are the tokens involved in
// the firing (may be nil for a boolean firing).
func NewEvent(ctx context.Context, eventType EventType, transition *Transition, from []Place, to []Place, tokens []Token, workflow *Workflow) *BaseEvent {
	return &BaseEvent{
		eventType:  eventType,
		transition: transition,
		from:       from,
		to:         to,
		tokens:     tokens,
		workflow:   workflow,
		ctx:        ctx,
	}
}

// Type returns the event type
func (e *BaseEvent) Type() EventType {
	return e.eventType
}

// Transition returns the transition associated with the event
func (e *BaseEvent) Transition() *Transition {
	return e.transition
}

// From returns the source places of the transition
func (e *BaseEvent) From() []Place {
	return e.from
}

// To returns the target places of the transition
func (e *BaseEvent) To() []Place {
	return e.to
}

// Tokens returns the tokens involved in this firing.
func (e *BaseEvent) Tokens() []Token {
	return e.tokens
}

// Workflow returns the workflow instance
func (e *BaseEvent) Workflow() *Workflow {
	return e.workflow
}

// Context returns the context.Context for the event
func (e *BaseEvent) Context() context.Context {
	return e.ctx
}

// GuardEvent represents a guard event in the workflow
type GuardEvent struct {
	BaseEvent
	isBlocking bool
}

// ListenerHandle represents a handle to remove a listener
// This provides a reliable way to remove listeners without needing the exact function value
type ListenerHandle struct {
	id        uint64
	eventType EventType
	owner     any // Pointer to the owner (Definition, Manager, or Workflow) for type safety
}

// NewGuardEvent creates a new Guard Event instance. tokens are the tokens
// involved in the firing (may be nil for a boolean firing); for per-token firing
// this is the single token being advanced, which the guard can inspect.
func NewGuardEvent(ctx context.Context, transition *Transition, from []Place, to []Place, tokens []Token, workflow *Workflow) *GuardEvent {
	return &GuardEvent{
		BaseEvent: BaseEvent{
			eventType:  EventGuard,
			transition: transition,
			from:       from,
			to:         to,
			tokens:     tokens,
			workflow:   workflow,
			ctx:        ctx,
		},
		isBlocking: false,
	}
}

// IsBlocking returns whether the event is blocking
func (e *GuardEvent) IsBlocking() bool {
	return e.isBlocking
}

// SetBlocking sets whether the event is blocking
func (e *GuardEvent) SetBlocking(blocking bool) {
	e.isBlocking = blocking
}

// EventListener is a function that handles workflow events
type EventListener func(Event) error

// GuardEventListener is a function that handles guard events
type GuardEventListener func(*GuardEvent) error

// Listener interface for handling events
type Listener interface {
	HandleEvent(Event) error
}

// GuardListener interface for handling guard events
type GuardListener interface {
	HandleGuardEvent(*GuardEvent) error
}
