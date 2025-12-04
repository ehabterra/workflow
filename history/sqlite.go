package history

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SQLiteHistory struct {
	db           *sql.DB
	table        string
	customFields map[string]string // key: field name, value: SQL column definition
}

type Option func(*SQLiteHistory)

func WithTable(name string) Option {
	return func(h *SQLiteHistory) { h.table = name }
}
func WithCustomFields(fields map[string]string) Option {
	return func(h *SQLiteHistory) { h.customFields = fields }
}

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

func (h *SQLiteHistory) Initialize() error {
	schema := h.GenerateSchema()
	_, err := h.db.Exec(schema)
	return err
}

func (h *SQLiteHistory) SaveTransition(record *TransitionRecord) error {
	cols := []string{"workflow_id", "from_state", "to_state", "transition", "notes", "actor", "created_at"}
	vals := []interface{}{record.WorkflowID, record.FromState, record.ToState, record.Transition, record.Notes, record.Actor, record.CreatedAt.Format(time.RFC3339)}
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
	_, err := h.db.Exec(query, vals...)
	return err
}

func (h *SQLiteHistory) ListHistory(workflowID string, opts QueryOptions) ([]TransitionRecord, error) {
	baseCols := []string{"workflow_id", "from_state", "to_state", "transition", "notes", "actor", "created_at"}
	customCols := []string{}
	for key := range h.customFields {
		customCols = append(customCols, key)
	}
	selectCols := append(baseCols, customCols...)

	where := []string{"workflow_id = ?"}
	args := []interface{}{workflowID}

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
	}
	if opts.Offset > 0 {
		sqlStr += fmt.Sprintf(" OFFSET %d", opts.Offset)
	}

	rows, err := h.db.Query(sqlStr, args...)
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
		scanArgs := []interface{}{&r.WorkflowID, &r.FromState, &r.ToState, &r.Transition, &r.Notes, &r.Actor, &createdAt}
		customVals := make([]interface{}, len(customCols))
		for i := range customVals {
			customVals[i] = new(interface{})
		}
		scanArgs = append(scanArgs, customVals...)
		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if len(customCols) > 0 {
			r.CustomFields = make(map[string]interface{})
			for i, col := range customCols {
				valPtr := customVals[i].(*interface{})
				r.CustomFields[col] = *valPtr
			}
		}
		history = append(history, r)
	}
	return history, nil
}
