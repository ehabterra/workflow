package storage

import (
	"encoding/json"
	"fmt"
)

// config holds the table and column configuration shared by the SQL-backed
// storage implementations. SQLiteStorage and PostgresStorage both embed it.
type config struct {
	table         string
	idColumn      string
	stateColumn   string
	versionColumn string

	// contextColumn stores the workflow's full context map as one JSON document,
	// so every SetContext key round-trips through save/load — not just the keys
	// configured as custom-field columns. Empty disables it (legacy tables).
	contextColumn string

	// customFields maps a context key to a full SQL column definition.
	// Example: {"document_id": "document_id TEXT", "approver": "approver TEXT"}
	customFields map[string]string
}

func defaultConfig() config {
	return config{
		table:         "workflow_states",
		idColumn:      "id",
		stateColumn:   "state",
		versionColumn: "version",
		contextColumn: "context",
		customFields:  make(map[string]string),
	}
}

// Option configures a SQL-backed storage implementation (SQLite or Postgres).
type Option func(*config)

// WithTable sets the name of the table used to store workflow state.
// Default: "workflow_states".
func WithTable(name string) Option {
	return func(c *config) { c.table = name }
}

// WithIDColumn sets the name of the column used to store the workflow ID.
// Default: "id".
func WithIDColumn(name string) Option {
	return func(c *config) { c.idColumn = name }
}

// WithStateColumn sets the name of the column used to store the workflow's
// current places (state). Default: "state".
func WithStateColumn(name string) Option {
	return func(c *config) { c.stateColumn = name }
}

// WithVersionColumn sets the name of the optimistic-concurrency version column.
// Default: "version".
func WithVersionColumn(name string) Option {
	return func(c *config) { c.versionColumn = name }
}

// WithContextColumn sets the name of the column that persists the workflow's
// full context map as a single JSON document. Default: "context".
//
// Pass an empty name to disable it (e.g. for a pre-existing table without the
// column) — then only the keys configured via WithCustomFields survive a
// save/load round-trip and every other context key is dropped on save.
func WithContextColumn(name string) Option {
	return func(c *config) { c.contextColumn = name }
}

// WithCustomFields defines the schema for additional application-specific data.
// The map key is the key used in the workflow's context map; the value is the
// full SQL column definition (e.g. "title TEXT", "amount INTEGER NOT NULL").
func WithCustomFields(fields map[string]string) Option {
	return func(c *config) { c.customFields = fields }
}

// customColumns returns the configured custom column names alongside the values
// encoded from ctxData for those columns, in a single consistent iteration order.
// The encode function performs backend-specific value coercion; missing keys and
// nil values become SQL NULL.
func (c config) customColumns(ctxData map[string]any, encode func(val any, present bool) any) (names []string, values []any) {
	names = make([]string, 0, len(c.customFields))
	values = make([]any, 0, len(c.customFields))
	for key, colDef := range c.customFields {
		names = append(names, firstField(colDef))
		val, ok := ctxData[key]
		values = append(values, encode(val, ok))
	}
	return names, values
}

// encodeContextJSON marshals the full context map for the context column.
// A nil/empty map encodes as "{}" so the NOT NULL DEFAULT '{}' column stays
// uniform. Values that JSON cannot represent (channels, funcs, …) are an error.
func encodeContextJSON(ctxData map[string]any) (string, error) {
	if len(ctxData) == 0 {
		return "{}", nil
	}
	data, err := json.Marshal(ctxData)
	if err != nil {
		return "", fmt.Errorf("failed to marshal context: %w", err)
	}
	return string(data), nil
}

// decodeContextJSON decodes the context column value (JSON as string or []byte;
// NULL/empty yields an empty map). Numbers decode to float64, per encoding/json.
func decodeContextJSON(val any) (map[string]any, error) {
	var data []byte
	switch v := val.(type) {
	case nil:
		return map[string]any{}, nil
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return nil, fmt.Errorf("unexpected type %T for context column", val)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	ctxData := make(map[string]any)
	if err := json.Unmarshal(data, &ctxData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal context: %w", err)
	}
	return ctxData, nil
}

// firstField returns the first whitespace-delimited token of a column
// definition — i.e. the column name from a definition like "amount INTEGER".
func firstField(colDef string) string {
	for i := 0; i < len(colDef); i++ {
		if colDef[i] == ' ' || colDef[i] == '\t' {
			return colDef[:i]
		}
	}
	return colDef
}
