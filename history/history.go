package history

import (
	"context"
	"time"
)

// TransitionRecord is the base struct for a transition event.
type TransitionRecord struct {
	WorkflowID   string
	FromState    string
	ToState      string
	Transition   string
	Notes        string
	Actor        string
	CreatedAt    time.Time
	CustomFields map[string]any // For custom columns, if any
}

// QueryOptions allows for pagination and filtering.
type QueryOptions struct {
	Limit      int
	Offset     int
	FromDate   *time.Time
	ToDate     *time.Time
	Actor      string
	Transition string
}

// HistoryStore is the interface for saving and querying transition history.
//
// SaveTransition, ListHistory and Initialize take a context.Context so callers
// can apply cancellation and deadlines; implementations honor it via the
// database/sql *Context methods.
type HistoryStore interface {
	SaveTransition(ctx context.Context, record *TransitionRecord) error
	ListHistory(ctx context.Context, workflowID string, opts QueryOptions) ([]TransitionRecord, error)
	GenerateSchema() string
	Initialize(ctx context.Context) error
}
