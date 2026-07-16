// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package history

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// execer abstracts the statement-execution method shared by *sql.DB and *sql.Tx,
// so a transition can be written either directly or inside a caller's transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// dialect captures the engine-specific pieces of SQL the history store needs:
// parameter placeholders, the auto-increment primary key, the timestamp column,
// and how time values are encoded for parameters.
type dialect struct {
	// placeholder returns the parameter placeholder for the n-th (1-based) arg.
	placeholder func(n int) string
	// idColumnDef is the auto-increment primary-key column definition.
	idColumnDef string
	// timeColumnDef is the created_at column definition with a now() default.
	timeColumnDef string
	// encodeTime converts a time.Time into the parameter value the engine stores.
	encodeTime func(time.Time) any
	// noLimit is the LIMIT clause used when only OFFSET is requested (SQLite
	// requires a LIMIT before OFFSET; PostgreSQL spells "no limit" differently).
	noLimit string
}

var sqliteDialect = dialect{
	placeholder:   func(int) string { return "?" },
	idColumnDef:   "id INTEGER PRIMARY KEY AUTOINCREMENT",
	timeColumnDef: "created_at DATETIME DEFAULT CURRENT_TIMESTAMP",
	encodeTime:    func(t time.Time) any { return t.Format(time.RFC3339) },
	noLimit:       "LIMIT -1",
}

var postgresDialect = dialect{
	placeholder:   func(n int) string { return fmt.Sprintf("$%d", n) },
	idColumnDef:   "id BIGSERIAL PRIMARY KEY",
	timeColumnDef: "created_at TIMESTAMPTZ DEFAULT now()",
	encodeTime:    func(t time.Time) any { return t },
	noLimit:       "LIMIT ALL",
}

// SQLHistory is a HistoryStore backed by a SQL database. Construct it with
// NewSQLiteHistory or NewPostgresHistory; the two differ only in dialect.
type SQLHistory struct {
	db           *sql.DB
	table        string
	customFields map[string]string // key: field name, value: SQL column definition
	dialect      dialect
}

// Option configures a SQLHistory.
type Option func(*SQLHistory)

// WithTable sets the name of the table used to store transition history.
// Default: "transition_history".
func WithTable(name string) Option {
	return func(h *SQLHistory) { h.table = name }
}

// WithCustomFields declares additional columns to persist alongside each record.
// The map key is the field name (matched against TransitionRecord.CustomFields);
// the value is the full SQL column definition (e.g. "ip_address TEXT").
func WithCustomFields(fields map[string]string) Option {
	return func(h *SQLHistory) { h.customFields = fields }
}

// NewSQLiteHistory creates a history store speaking SQLite's dialect.
func NewSQLiteHistory(db *sql.DB, opts ...Option) *SQLHistory {
	return newSQLHistory(db, sqliteDialect, opts)
}

// NewPostgresHistory creates a history store speaking PostgreSQL's dialect
// ($N placeholders, BIGSERIAL primary key, TIMESTAMPTZ timestamps). Use it with
// any database/sql driver that speaks PostgreSQL (pgx stdlib recommended), and
// share the *sql.DB with a storage.PostgresStorage to commit state and history
// atomically (SaveTransitionTx + storage.RunInTx, or Manager.Execute with
// workflow.WithTxSideEffect).
func NewPostgresHistory(db *sql.DB, opts ...Option) *SQLHistory {
	return newSQLHistory(db, postgresDialect, opts)
}

func newSQLHistory(db *sql.DB, d dialect, opts []Option) *SQLHistory {
	h := &SQLHistory{
		db:           db,
		table:        "transition_history",
		customFields: map[string]string{},
		dialect:      d,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// GenerateSchema returns the CREATE TABLE statement for the history table,
// including any configured custom-field columns.
func (h *SQLHistory) GenerateSchema() string {
	columns := []string{
		h.dialect.idColumnDef,
		"workflow_id TEXT NOT NULL",
		"from_state TEXT NOT NULL",
		"to_state TEXT NOT NULL",
		"transition TEXT NOT NULL",
		"notes TEXT",
		"actor TEXT",
		h.dialect.timeColumnDef,
	}
	for _, colDef := range h.customFields {
		columns = append(columns, colDef)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s);", h.table, strings.Join(columns, ", "))
}

// Initialize creates the history table if it does not already exist.
func (h *SQLHistory) Initialize(ctx context.Context) error {
	schema := h.GenerateSchema()
	_, err := h.db.ExecContext(ctx, schema)
	return err
}

// SaveTransition appends a single transition record to the history table.
func (h *SQLHistory) SaveTransition(ctx context.Context, record *TransitionRecord) error {
	return h.saveTransition(ctx, h.db, record)
}

// SaveTransitionTx behaves like SaveTransition but writes through the provided
// transaction, so a history record can be committed atomically with a state
// change. The same *sql.DB must back the state store for the writes to share
// the transaction. See storage.RunInTx and workflow.WithTxSideEffect.
func (h *SQLHistory) SaveTransitionTx(ctx context.Context, tx *sql.Tx, record *TransitionRecord) error {
	return h.saveTransition(ctx, tx, record)
}

func (h *SQLHistory) saveTransition(ctx context.Context, q execer, record *TransitionRecord) error {
	cols := []string{"workflow_id", "from_state", "to_state", "transition", "notes", "actor", "created_at"}
	vals := []any{record.WorkflowID, record.FromState, record.ToState, record.Transition, record.Notes, record.Actor, h.dialect.encodeTime(record.CreatedAt)}

	// Add custom fields if present in record.CustomFields
	for key := range h.customFields {
		if record.CustomFields != nil {
			if val, ok := record.CustomFields[key]; ok {
				cols = append(cols, key)
				vals = append(vals, val)
			}
		}
	}

	placeholders := make([]string, len(vals))
	for i := range placeholders {
		placeholders[i] = h.dialect.placeholder(i + 1)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", h.table, strings.Join(cols, ","), strings.Join(placeholders, ","))
	_, err := q.ExecContext(ctx, query, vals...)
	return err
}

// ListHistory returns the transition records for a workflow, most recent first,
// honoring the filtering and pagination options in opts.
func (h *SQLHistory) ListHistory(ctx context.Context, workflowID string, opts QueryOptions) (records []TransitionRecord, err error) {
	baseCols := []string{"workflow_id", "from_state", "to_state", "transition", "notes", "actor", "created_at"}
	customCols := []string{}
	for key := range h.customFields {
		customCols = append(customCols, key)
	}
	selectCols := append(baseCols, customCols...)

	args := []any{workflowID}
	where := []string{fmt.Sprintf("workflow_id = %s", h.dialect.placeholder(1))}
	addWhere := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, h.dialect.placeholder(len(args))))
	}

	if opts.Actor != "" {
		addWhere("actor = %s", opts.Actor)
	}
	if opts.Transition != "" {
		addWhere("transition = %s", opts.Transition)
	}
	if opts.FromDate != nil {
		addWhere("created_at >= %s", h.dialect.encodeTime(*opts.FromDate))
	}
	if opts.ToDate != nil {
		addWhere("created_at <= %s", h.dialect.encodeTime(*opts.ToDate))
	}

	sqlStr := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY id DESC", strings.Join(selectCols, ", "), h.table, strings.Join(where, " AND "))
	if opts.Limit > 0 {
		sqlStr += fmt.Sprintf(" LIMIT %d", opts.Limit)
		if opts.Offset > 0 {
			sqlStr += fmt.Sprintf(" OFFSET %d", opts.Offset)
		}
	} else if opts.Offset > 0 {
		sqlStr += fmt.Sprintf(" %s OFFSET %d", h.dialect.noLimit, opts.Offset)
	}

	rows, err := h.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("closing rows: %w", closeErr))
		}
	}()

	for rows.Next() {
		var r TransitionRecord
		var createdAt any
		scanArgs := []any{&r.WorkflowID, &r.FromState, &r.ToState, &r.Transition, &r.Notes, &r.Actor, &createdAt}
		customVals := make([]any, len(customCols))
		for i := range customVals {
			customVals[i] = new(any)
		}
		scanArgs = append(scanArgs, customVals...)
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		r.CreatedAt = decodeTime(createdAt)
		if len(customCols) > 0 {
			r.CustomFields = make(map[string]any)
			for i, col := range customCols {
				valPtr := customVals[i].(*any)
				r.CustomFields[col] = *valPtr
			}
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// decodeTime coerces a scanned created_at value: SQLite hands back the stored
// RFC3339 string ([]byte or string), PostgreSQL a time.Time.
func decodeTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t
	case string:
		parsed, _ := time.Parse(time.RFC3339, t)
		return parsed
	case []byte:
		parsed, _ := time.Parse(time.RFC3339, string(t))
		return parsed
	default:
		return time.Time{}
	}
}
