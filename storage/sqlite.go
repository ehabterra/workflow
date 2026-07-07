package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
)

// minUnixNanoTime and maxUnixNanoTime bound the instants representable as int64
// Unix nanoseconds. Times outside this range (before ~1677 or after ~2262) would
// overflow time.Time.UnixNano and wrap to the wrong sign; clampUnixNano saturates
// them instead so due-index ordering and comparisons stay monotonic.
var (
	minUnixNanoTime = time.Unix(0, math.MinInt64)
	maxUnixNanoTime = time.Unix(0, math.MaxInt64)
)

// clampUnixNano returns t as int64 Unix nanoseconds, saturating to math.MaxInt64
// (or math.MinInt64) for instants beyond the representable range rather than
// letting UnixNano silently wrap negative.
func clampUnixNano(t time.Time) int64 {
	switch {
	case t.After(maxUnixNanoTime):
		return math.MaxInt64
	case t.Before(minUnixNanoTime):
		return math.MinInt64
	default:
		return t.UnixNano()
	}
}

// Compile-time assertions that SQLiteStorage satisfies the full storage
// contract, including the host-driven-timer (M4) due index.
var _ workflow.TransactionalDueStorage = (*SQLiteStorage)(nil)

// SQLiteStorage provides a persistent storage implementation using SQLite.
// It is highly configurable to allow for custom table and column names,
// as well as storing arbitrary application-specific data alongside the
// workflow state. It implements workflow.TransactionalDueStorage (and thus
// workflow.Storage with built-in optimistic concurrency).
type SQLiteStorage struct {
	db *sql.DB
	config
}

// NewSQLiteStorage creates a new SQLiteStorage with the given options.
func NewSQLiteStorage(db *sql.DB, opts ...Option) (*SQLiteStorage, error) {
	if db == nil {
		return nil, fmt.Errorf("db cannot be nil")
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return &SQLiteStorage{db: db, config: cfg}, nil
}

// GenerateSchema returns the `CREATE TABLE` SQL statement based on the storage configuration.
// This allows the user to see, modify, or use the schema with a separate migration tool.
func (s *SQLiteStorage) GenerateSchema() string {
	columns := []string{
		fmt.Sprintf("%s TEXT PRIMARY KEY", s.idColumn),
		fmt.Sprintf("%s TEXT NOT NULL", s.stateColumn),
		fmt.Sprintf("%s INTEGER NOT NULL DEFAULT 0", s.versionColumn),
	}
	if s.contextColumn != "" {
		columns = append(columns, fmt.Sprintf("%s TEXT NOT NULL DEFAULT '{}'", s.contextColumn))
	}
	if s.dueColumn != "" {
		// Nullable: NULL means "no timer running", so the instance never matches
		// ListDue. Stored as INTEGER Unix nanoseconds so comparisons are exact.
		columns = append(columns, fmt.Sprintf("%s INTEGER", s.dueColumn))
	}

	for _, colDef := range s.customFields {
		columns = append(columns, colDef)
	}

	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s);", s.table, strings.Join(columns, ", "))
}

// EnsureSchema creates the state table if it does not exist and idempotently
// applies the migrations this library version needs — currently the M4 due
// index: it adds the due column to a pre-existing table (safely tolerating a
// table that already has it) and creates the supporting index. It is safe to
// call on every process start against both fresh and pre-existing tables, and
// is the recommended one-call setup for backends that use Manager.FireDue.
func (s *SQLiteStorage) EnsureSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, s.GenerateSchema()); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	if s.dueColumn == "" {
		return nil
	}
	// A pre-existing table won't gain the column from CREATE TABLE IF NOT EXISTS;
	// add it, tolerating "duplicate column" so the call stays idempotent (SQLite
	// has no ADD COLUMN IF NOT EXISTS).
	alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s INTEGER", s.table, s.dueColumn)
	if _, err := s.db.ExecContext(ctx, alter); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("add due column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, s.dueIndexDDL()); err != nil {
		return fmt.Errorf("create due index: %w", err)
	}
	return nil
}

// encodeValue converts a context value into a form SQLite can store. SQLite has
// no native boolean, so bools become 0/1; slices and maps are JSON-encoded.
func encodeValue(val any, present bool) any {
	if !present || val == nil {
		return nil
	}
	switch value := val.(type) {
	case []string, []any, map[string]any:
		if jsonBytes, err := json.Marshal(value); err == nil {
			return string(jsonBytes)
		}
		return nil
	case bool:
		if value {
			return 1
		}
		return 0
	default:
		return val
	}
}

// LoadState loads the workflow's marking, context, and optimistic-concurrency
// version in ONE query. Reading the version separately would allow read skew
// under concurrency: a marking from version N paired with version N+1 read
// after a concurrent commit, which makes a later stale save pass the version
// check and lose the concurrent update.
func (s *SQLiteStorage) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	return s.loadState(ctx, s.db, id)
}

// LoadStateTx behaves like LoadState but reads through the provided transaction,
// so it observes the transaction's own uncommitted writes.
func (s *SQLiteStorage) LoadStateTx(ctx context.Context, tx *sql.Tx, id string) (workflow.Marking, map[string]any, int64, error) {
	return s.loadState(ctx, tx, id)
}

func (s *SQLiteStorage) loadState(ctx context.Context, q querier, id string) (workflow.Marking, map[string]any, int64, error) {
	columns := []string{s.stateColumn}
	if s.contextColumn != "" {
		columns = append(columns, s.contextColumn)
	}
	customStart := len(columns)
	customFieldKeys := make([]string, 0, len(s.customFields))

	for key, colDef := range s.customFields {
		colName := strings.Fields(colDef)[0]
		columns = append(columns, colName)
		customFieldKeys = append(customFieldKeys, key)
	}
	versionIdx := len(columns)
	columns = append(columns, s.versionColumn)

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?",
		strings.Join(columns, ", "),
		s.table,
		s.idColumn,
	)

	row := q.QueryRowContext(ctx, query, id)

	// Prepare to scan into a slice of any pointers
	scanArgs := make([]any, len(columns))
	for i := range columns {
		scanArgs[i] = new(any)
	}

	err := row.Scan(scanArgs...)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, 0, fmt.Errorf("%w: id %s", workflow.ErrWorkflowNotFound, id)
		}
		return nil, nil, 0, fmt.Errorf("failed to load state: %w", err)
	}

	// Process results
	var stateJSON []byte
	if rawState, ok := (*scanArgs[0].(*any)).([]byte); ok {
		stateJSON = rawState
	} else {
		return nil, nil, 0, fmt.Errorf("unexpected type for state column")
	}

	marking, err := workflow.UnmarshalMarkingJSON(stateJSON)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	version, ok := (*(scanArgs[versionIdx].(*any))).(int64)
	if !ok {
		return nil, nil, 0, fmt.Errorf("unexpected type for version column")
	}

	// The context column holds the full context map; custom-field columns are
	// queryable projections of individual keys and are overlaid afterwards so
	// their typed values (e.g. INTEGER int64) win over the JSON decoding.
	context := make(map[string]any)
	if s.contextColumn != "" {
		context, err = decodeContextJSON(*(scanArgs[1].(*any)))
		if err != nil {
			return nil, nil, 0, err
		}
	}

	for i, key := range customFieldKeys {
		val := *(scanArgs[customStart+i].(*any))

		// Check if this is a JSON string that should be unmarshaled
		// This handles fields like "roles" (arrays) and "nested" (maps) which are stored as JSON
		if strVal, ok := val.(string); ok && strVal != "" {
			// Try to unmarshal as JSON array first
			var jsonArray []any
			if err := json.Unmarshal([]byte(strVal), &jsonArray); err == nil {
				// Successfully unmarshaled as JSON array
				// Try to convert to []string if all elements are strings
				stringArray := make([]string, 0, len(jsonArray))
				allStrings := true
				for _, item := range jsonArray {
					if str, ok := item.(string); ok {
						stringArray = append(stringArray, str)
					} else {
						allStrings = false
						break
					}
				}
				if allStrings && len(stringArray) > 0 {
					context[key] = stringArray
				} else {
					context[key] = jsonArray
				}
			} else {
				// Try to unmarshal as JSON object (map)
				var jsonMap map[string]any
				if err := json.Unmarshal([]byte(strVal), &jsonMap); err == nil {
					context[key] = jsonMap
				} else {
					// Not JSON, store as string
					context[key] = strVal
				}
			}
		} else if val != nil {
			// SQLite may return int64 for INTEGER columns, etc.
			// Check if this is a boolean stored as integer (1 or 0)
			if intVal, ok := val.(int64); ok {
				// Check if the column definition suggests this is a boolean
				// For now, we'll check if the value is 0 or 1 and the key suggests boolean
				// This is a heuristic - in practice, you'd check the column definition
				if (key == "bool" || key == "boolean") && (intVal == 0 || intVal == 1) {
					context[key] = intVal == 1
				} else {
					context[key] = val
				}
			} else {
				context[key] = val
			}
		} else {
			context[key] = nil
		}
	}

	return marking, context, version, nil
}

// ListIDs implements workflow.ListableStorage, returning persisted workflow IDs
// ordered by ID for stable pagination. A zero opts.Limit means no limit.
func (s *SQLiteStorage) ListIDs(ctx context.Context, opts workflow.ListOptions) ([]string, error) {
	query := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", s.idColumn, s.table, s.idColumn)
	var args []any
	if opts.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, opts.Limit, opts.Offset)
	} else if opts.Offset > 0 {
		query += " LIMIT -1 OFFSET ?" // SQLite requires a LIMIT before OFFSET
		args = append(args, opts.Offset)
	}
	return scanIDs(s.db.QueryContext(ctx, query, args...))
}

// ListDue implements workflow.DueStorage, returning the IDs of instances whose
// stored next-due time is non-null and at or before `before`, ordered by due
// time ascending then by ID. A zero limit means no limit.
func (s *SQLiteStorage) ListDue(ctx context.Context, before time.Time, limit int) ([]string, error) {
	if s.dueColumn == "" {
		return nil, fmt.Errorf("due index disabled (empty due column): %w", errors.ErrUnsupported)
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL AND %s <= ? ORDER BY %s ASC, %s ASC",
		s.idColumn, s.table, s.dueColumn, s.dueColumn, s.dueColumn, s.idColumn)
	args := []any{clampUnixNano(before.UTC())}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	return scanIDs(s.db.QueryContext(ctx, query, args...))
}

// dueValueSQLite encodes a next-due time for the SQLite due column: SQL NULL
// when no timer runs, otherwise Unix nanoseconds (UTC) so range comparisons and
// ordering are exact.
func dueValueSQLite(due *time.Time) any {
	if due == nil {
		return nil
	}
	return clampUnixNano(due.UTC())
}

// DeleteState removes a workflow's state from the database.
func (s *SQLiteStorage) DeleteState(ctx context.Context, id string) error {
	return s.deleteState(ctx, s.db, id)
}

// DeleteStateTx behaves like DeleteState but writes through the provided transaction.
func (s *SQLiteStorage) DeleteStateTx(ctx context.Context, tx *sql.Tx, id string) error {
	return s.deleteState(ctx, tx, id)
}

func (s *SQLiteStorage) deleteState(ctx context.Context, q querier, id string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", s.table, s.idColumn)
	_, err := q.ExecContext(ctx, query, id)
	return err
}

// SaveState implements workflow.Storage. It saves the workflow
// only if the stored version equals expectedVersion, returning the new version.
// Pass expectedVersion 0 to create a new workflow. A mismatch returns
// workflow.ErrConflict.
//
// It preserves the due column (leaves it untouched on update, NULL on insert) but
// does not maintain the due index; for a timed definition, use
// SaveStateWithDue or go through the Manager so the index stays current.
func (s *SQLiteStorage) SaveState(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64) (int64, error) {
	return s.saveState(ctx, s.db, id, marking, ctxData, expectedVersion, nil, false)
}

// SaveStateTx behaves like SaveState but writes through the
// provided transaction, so a versioned state change and a history record can be
// committed atomically. See RunInTx. Like SaveState it does not maintain
// the due index; for a timed definition composed manually into a transaction, use
// SaveStateInTxWithDue (or the Manager) so the due index commits with it.
func (s *SQLiteStorage) SaveStateTx(ctx context.Context, tx *sql.Tx, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64) (int64, error) {
	return s.saveState(ctx, tx, id, marking, ctxData, expectedVersion, nil, false)
}

// SaveStateInTx implements workflow.TransactionalStorage: the versioned
// save and every side effect run in one transaction, committing only if all
// succeed. Effects receive the *sql.Tx (as an any). It does not maintain the due
// index; for a timed definition use SaveStateInTxWithDue so the index
// commits atomically with state and effects.
func (s *SQLiteStorage) SaveStateInTx(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, effects ...workflow.TxSideEffect) (int64, error) {
	return saveStateInTx(ctx, s.db, effects, func(tx *sql.Tx) (int64, error) {
		return s.saveState(ctx, tx, id, marking, ctxData, expectedVersion, nil, false)
	})
}

// SaveStateWithDue implements workflow.DueStorage: it saves the
// versioned state and records the instance's next-due time (nil clears it) in
// the due-index column.
func (s *SQLiteStorage) SaveStateWithDue(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time) (int64, error) {
	return s.saveState(ctx, s.db, id, marking, ctxData, expectedVersion, due, true)
}

// SaveStateInTxWithDue implements workflow.TransactionalDueStorage: the
// versioned save, the due-index update, and every side effect run in one
// transaction, committing only if all succeed.
func (s *SQLiteStorage) SaveStateInTxWithDue(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time, effects ...workflow.TxSideEffect) (int64, error) {
	return saveStateInTx(ctx, s.db, effects, func(tx *sql.Tx) (int64, error) {
		return s.saveState(ctx, tx, id, marking, ctxData, expectedVersion, due, true)
	})
}

func (s *SQLiteStorage) saveState(ctx context.Context, q querier, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time, setDue bool) (int64, error) {
	stateJSON, err := json.Marshal(marking)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal state: %w", err)
	}
	customCols, customVals := s.customColumns(ctxData, encodeValue)
	if s.contextColumn != "" {
		ctxJSON, err := encodeContextJSON(ctxData)
		if err != nil {
			return 0, err
		}
		customCols = append([]string{s.contextColumn}, customCols...)
		customVals = append([]any{ctxJSON}, customVals...)
	}
	// Maintain the due index only on the WithDue paths; the plain paths leave the
	// column untouched (preserved on update, NULL on insert).
	if setDue && s.dueColumn != "" {
		customCols = append(customCols, s.dueColumn)
		customVals = append(customVals, dueValueSQLite(due))
	}

	if expectedVersion <= 0 {
		// Create a new row at version 1; do nothing if the id already exists so we
		// can distinguish "created" (1 row affected) from "already exists" (0).
		columns := append([]string{s.idColumn, s.stateColumn, s.versionColumn}, customCols...)
		values := append([]any{id, stateJSON, int64(1)}, customVals...)
		placeholders := make([]string, len(columns))
		for i := range placeholders {
			placeholders[i] = "?"
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT(%s) DO NOTHING;",
			s.table, strings.Join(columns, ", "), strings.Join(placeholders, ", "), s.idColumn)

		res, err := q.ExecContext(ctx, query, values...)
		if err != nil {
			return 0, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		if n == 0 {
			return 0, fmt.Errorf("%w: workflow %s already exists (expected version 0)", workflow.ErrConflict, id)
		}
		return 1, nil
	}

	// Update the existing row only if its version still matches, bumping the version.
	setClauses := []string{
		fmt.Sprintf("%s = ?", s.stateColumn),
		fmt.Sprintf("%s = %s + 1", s.versionColumn, s.versionColumn),
	}
	args := []any{stateJSON}
	for i, col := range customCols {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", col))
		args = append(args, customVals[i])
	}
	args = append(args, id, expectedVersion)

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = ? AND %s = ?;",
		s.table, strings.Join(setClauses, ", "), s.idColumn, s.versionColumn)

	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("%w: workflow %s (expected version %d)", workflow.ErrConflict, id, expectedVersion)
	}
	return expectedVersion + 1, nil
}
