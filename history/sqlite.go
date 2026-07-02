package history

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// execer abstracts the statement-execution method shared by *sql.DB and *sql.Tx,
// so a transition can be written either directly or inside a caller's transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SQLiteHistory is a SQLite-backed implementation of HistoryStore. It records
// every transition in a configurable table and supports additional custom columns.
type SQLiteHistory struct {
	db           *sql.DB
	table        string
	customFields map[string]string // key: field name, value: SQL column definition
}

// Option configures a SQLiteHistory.
type Option func(*SQLiteHistory)

// WithTable sets the name of the table used to store transition history.
// Default: "transition_history".
func WithTable(name string) Option {
	return func(h *SQLiteHistory) { h.table = name }
}

// WithCustomFields declares additional columns to persist alongside each record.
// The map key is the field name (matched against TransitionRecord.CustomFields);
// the value is the full SQL column definition (e.g. "ip_address TEXT").
func WithCustomFields(fields map[string]string) Option {
	return func(h *SQLiteHistory) { h.customFields = fields }
}

// NewSQLiteHistory creates a SQLiteHistory using the given database handle and options.
func NewSQLiteHistory(db *sql.DB, opts ...Option) *SQLiteHistory {
	h := &SQLiteHistory{
		db:           db,
		table:        "transition_history",
		customFields: map[string]string{},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// GenerateSchema returns the CREATE TABLE statement for the history table,
// including any configured custom-field columns.
func (h *SQLiteHistory) GenerateSchema() string {
	columns := []string{
		"id INTEGER PRIMARY KEY AUTOINCREMENT",
		"workflow_id TEXT NOT NULL",
		"from_state TEXT NOT NULL",
		"to_state TEXT NOT NULL",
		"transition TEXT NOT NULL",
		"notes TEXT",
		"actor TEXT",
		"created_at DATETIME DEFAULT CURRENT_TIMESTAMP",
	}
	for _, colDef := range h.customFields {
		columns = append(columns, colDef)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s);", h.table, strings.Join(columns, ", "))
}

// Initialize creates the history table if it does not already exist.
func (h *SQLiteHistory) Initialize(ctx context.Context) error {
	schema := h.GenerateSchema()
	_, err := h.db.ExecContext(ctx, schema)
	return err
}

// SaveTransition appends a single transition record to the history table.
func (h *SQLiteHistory) SaveTransition(ctx context.Context, record *TransitionRecord) error {
	return h.saveTransition(ctx, h.db, record)
}

// SaveTransitionTx behaves like SaveTransition but writes through the provided
// transaction, so a history record can be committed atomically with a state
// change. The same *sql.DB must back the state store for the writes to share
// the transaction. See storage.RunInTx.
func (h *SQLiteHistory) SaveTransitionTx(ctx context.Context, tx *sql.Tx, record *TransitionRecord) error {
	return h.saveTransition(ctx, tx, record)
}

func (h *SQLiteHistory) saveTransition(ctx context.Context, q execer, record *TransitionRecord) error {
	cols := []string{"workflow_id", "from_state", "to_state", "transition", "notes", "actor", "created_at"}
	vals := []any{record.WorkflowID, record.FromState, record.ToState, record.Transition, record.Notes, record.Actor, record.CreatedAt.Format(time.RFC3339)}
	placeholders := []string{"?", "?", "?", "?", "?", "?", "?"}

	// Add custom fields if present in record.CustomFields
	for key := range h.customFields {
		if record.CustomFields != nil {
			if val, ok := record.CustomFields[key]; ok {
				cols = append(cols, key)
				vals = append(vals, val)
				placeholders = append(placeholders, "?")
			}
		}
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", h.table, strings.Join(cols, ","), strings.Join(placeholders, ","))
	_, err := q.ExecContext(ctx, query, vals...)
	return err
}

// ListHistory returns the transition records for a workflow, most recent first,
// honoring the filtering and pagination options in opts.
func (h *SQLiteHistory) ListHistory(ctx context.Context, workflowID string, opts QueryOptions) ([]TransitionRecord, error) {
	baseCols := []string{"workflow_id", "from_state", "to_state", "transition", "notes", "actor", "created_at"}
	customCols := []string{}
	for key := range h.customFields {
		customCols = append(customCols, key)
	}
	selectCols := append(baseCols, customCols...)

	where := []string{"workflow_id = ?"}
	args := []any{workflowID}

	if opts.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, opts.Actor)
	}
	if opts.Transition != "" {
		where = append(where, "transition = ?")
		args = append(args, opts.Transition)
	}
	if opts.FromDate != nil {
		where = append(where, "created_at >= ?")
		args = append(args, opts.FromDate.Format(time.RFC3339))
	}
	if opts.ToDate != nil {
		where = append(where, "created_at <= ?")
		args = append(args, opts.ToDate.Format(time.RFC3339))
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY id DESC", strings.Join(selectCols, ", "), h.table, strings.Join(where, " AND "))
	if opts.Limit > 0 {
		sqlStr += fmt.Sprintf(" LIMIT %d", opts.Limit)
		if opts.Offset > 0 {
			sqlStr += fmt.Sprintf(" OFFSET %d", opts.Offset)
		}
	} else if opts.Offset > 0 {
		// OFFSET requires LIMIT in SQLite, use a large limit if none specified
		sqlStr += " LIMIT -1 OFFSET " + fmt.Sprintf("%d", opts.Offset)
	}

	rows, err := h.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			// Log error but don't fail the operation
			fmt.Printf("Error closing rows: %v\n", closeErr)
		}
	}()
	var history []TransitionRecord
	for rows.Next() {
		var r TransitionRecord
		var createdAt string
		scanArgs := []any{&r.WorkflowID, &r.FromState, &r.ToState, &r.Transition, &r.Notes, &r.Actor, &createdAt}
		customVals := make([]any, len(customCols))
		for i := range customVals {
			customVals[i] = new(any)
		}
		scanArgs = append(scanArgs, customVals...)
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if len(customCols) > 0 {
			r.CustomFields = make(map[string]any)
			for i, col := range customCols {
				valPtr := customVals[i].(*any)
				r.CustomFields[col] = *valPtr
			}
		}
		history = append(history, r)
	}
	return history, nil
}
