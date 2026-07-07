package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ehabterra/workflow"
)

// Compile-time assertion that PostgresStorage satisfies the full storage
// contract, including the host-driven-timer (M4) due index.
var _ workflow.TransactionalDueStorage = (*PostgresStorage)(nil)

// PostgresStorage is a PostgreSQL-backed implementation of workflow.Storage and
// It mirrors SQLiteStorage but uses PostgreSQL syntax
// ($N placeholders and INSERT ... ON CONFLICT upserts).
//
// Use it with any database/sql driver that speaks PostgreSQL; the pgx stdlib
// adapter is recommended:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//	db, _ := sql.Open("pgx", dsn)
//	store, _ := storage.NewPostgresStorage(db)
//	_ = store.EnsureSchema(context.Background())
type PostgresStorage struct {
	db *sql.DB
	config
}

// NewPostgresStorage creates a new PostgresStorage with the given options.
func NewPostgresStorage(db *sql.DB, opts ...Option) (*PostgresStorage, error) {
	if db == nil {
		return nil, fmt.Errorf("db cannot be nil")
	}

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	return &PostgresStorage{db: db, config: cfg}, nil
}

// GenerateSchema returns the CREATE TABLE statement for the state table.
func (s *PostgresStorage) GenerateSchema() string {
	columns := []string{
		fmt.Sprintf("%s TEXT PRIMARY KEY", s.idColumn),
		fmt.Sprintf("%s TEXT NOT NULL", s.stateColumn),
		fmt.Sprintf("%s BIGINT NOT NULL DEFAULT 0", s.versionColumn),
	}
	if s.contextColumn != "" {
		columns = append(columns, fmt.Sprintf("%s JSONB NOT NULL DEFAULT '{}'", s.contextColumn))
	}
	if s.dueColumn != "" {
		// Nullable: NULL means "no timer running", so the instance never matches
		// ListDue. TIMESTAMPTZ is natively comparable and indexable.
		columns = append(columns, fmt.Sprintf("%s TIMESTAMPTZ", s.dueColumn))
	}
	for _, colDef := range s.customFields {
		columns = append(columns, colDef)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s);", s.table, strings.Join(columns, ", "))
}

// EnsureSchema creates the state table if it does not exist and idempotently
// applies the migrations this library version needs — currently the M4 due
// index: it adds the due column to a pre-existing table (ADD COLUMN IF NOT
// EXISTS) and creates the supporting index. It is safe to call on every process
// start against both fresh and pre-existing tables, and is the recommended
// one-call setup for backends that use Manager.FireDue.
func (s *PostgresStorage) EnsureSchema(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, s.GenerateSchema()); err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	if s.dueColumn == "" {
		return nil
	}
	alter := fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s TIMESTAMPTZ", s.table, s.dueColumn)
	if _, err := s.db.ExecContext(ctx, alter); err != nil {
		return fmt.Errorf("add due column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, s.dueIndexDDL()); err != nil {
		return fmt.Errorf("create due index: %w", err)
	}
	return nil
}

// LoadState loads the workflow's marking, context, and optimistic-concurrency
// version in ONE query. Reading the version separately would allow read skew
// under concurrency: a marking from version N paired with version N+1 read
// after a concurrent commit, which makes a later stale save pass the version
// check and lose the concurrent update.
func (s *PostgresStorage) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	return s.loadState(ctx, s.db, id)
}

// LoadStateTx behaves like LoadState but reads through the provided transaction,
// so it observes the transaction's own uncommitted writes.
func (s *PostgresStorage) LoadStateTx(ctx context.Context, tx *sql.Tx, id string) (workflow.Marking, map[string]any, int64, error) {
	return s.loadState(ctx, tx, id)
}

func (s *PostgresStorage) loadState(ctx context.Context, q querier, id string) (workflow.Marking, map[string]any, int64, error) {
	columns := []string{s.stateColumn}
	if s.contextColumn != "" {
		columns = append(columns, s.contextColumn)
	}
	customStart := len(columns)
	customKeys := make([]string, 0, len(s.customFields))
	for key, colDef := range s.customFields {
		columns = append(columns, firstField(colDef))
		customKeys = append(customKeys, key)
	}
	versionIdx := len(columns)
	columns = append(columns, s.versionColumn)

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1", strings.Join(columns, ", "), s.table, s.idColumn)

	var stateJSON string
	scanArgs := make([]any, len(columns))
	scanArgs[0] = &stateJSON
	var ctxJSON any
	if s.contextColumn != "" {
		scanArgs[1] = &ctxJSON
	}
	customVals := make([]any, len(customKeys))
	for i := range customVals {
		scanArgs[customStart+i] = &customVals[i]
	}
	var version int64
	scanArgs[versionIdx] = &version

	if err := q.QueryRowContext(ctx, query, id).Scan(scanArgs...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, 0, fmt.Errorf("%w: id %s", workflow.ErrWorkflowNotFound, id)
		}
		return nil, nil, 0, fmt.Errorf("failed to load state: %w", err)
	}

	marking, err := workflow.UnmarshalMarkingJSON([]byte(stateJSON))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	// The context column holds the full context map; custom-field columns are
	// queryable projections of individual keys and are overlaid afterwards so
	// their natively-typed values win over the JSON decoding.
	ctxData := make(map[string]any, len(customKeys))
	if s.contextColumn != "" {
		ctxData, err = decodeContextJSON(ctxJSON)
		if err != nil {
			return nil, nil, 0, err
		}
	}
	for i, key := range customKeys {
		ctxData[key] = decodeValue(customVals[i])
	}
	return marking, ctxData, version, nil
}

// ListIDs implements workflow.ListableStorage, returning persisted workflow IDs
// ordered by ID for stable pagination. A zero opts.Limit means no limit.
func (s *PostgresStorage) ListIDs(ctx context.Context, opts workflow.ListOptions) ([]string, error) {
	query := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", s.idColumn, s.table, s.idColumn)
	var args []any
	if opts.Limit > 0 {
		args = append(args, opts.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if opts.Offset > 0 {
		args = append(args, opts.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}
	return scanIDs(s.db.QueryContext(ctx, query, args...))
}

// ListDue implements workflow.DueStorage, returning the IDs of instances whose
// stored next-due time is non-null and at or before `before`, ordered by due
// time ascending then by ID. A zero limit means no limit.
func (s *PostgresStorage) ListDue(ctx context.Context, before time.Time, limit int) ([]string, error) {
	if s.dueColumn == "" {
		return nil, fmt.Errorf("due index disabled (empty due column): %w", errors.ErrUnsupported)
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL AND %s <= $1 ORDER BY %s ASC, %s ASC",
		s.idColumn, s.table, s.dueColumn, s.dueColumn, s.dueColumn, s.idColumn)
	args := []any{before.UTC()}
	if limit > 0 {
		args = append(args, limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	return scanIDs(s.db.QueryContext(ctx, query, args...))
}

// dueValuePg encodes a next-due time for the Postgres due column: SQL NULL when
// no timer runs, otherwise the time itself (stored as UTC TIMESTAMPTZ).
func dueValuePg(due *time.Time) any {
	if due == nil {
		return nil
	}
	return due.UTC()
}

// DeleteState removes a workflow's state.
func (s *PostgresStorage) DeleteState(ctx context.Context, id string) error {
	return s.deleteState(ctx, s.db, id)
}

// DeleteStateTx behaves like DeleteState but writes through the provided transaction.
func (s *PostgresStorage) DeleteStateTx(ctx context.Context, tx *sql.Tx, id string) error {
	return s.deleteState(ctx, tx, id)
}

func (s *PostgresStorage) deleteState(ctx context.Context, q querier, id string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE %s = $1", s.table, s.idColumn)
	_, err := q.ExecContext(ctx, query, id)
	return err
}

// SaveState implements workflow.Storage. It preserves the due
// column (untouched on update, NULL on insert) but does not maintain the due
// index; for a timed definition, use SaveStateWithDue or go through the
// Manager so the index stays current.
func (s *PostgresStorage) SaveState(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64) (int64, error) {
	return s.saveState(ctx, s.db, id, marking, ctxData, expectedVersion, nil, false)
}

// SaveStateTx behaves like SaveState but writes through the
// provided transaction. Like SaveState it does not maintain the due
// index; for a timed definition composed manually into a transaction, use
// SaveStateInTxWithDue (or the Manager) so the due index commits with it.
func (s *PostgresStorage) SaveStateTx(ctx context.Context, tx *sql.Tx, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64) (int64, error) {
	return s.saveState(ctx, tx, id, marking, ctxData, expectedVersion, nil, false)
}

// SaveStateInTx implements workflow.TransactionalStorage: the versioned
// save and every side effect run in one transaction, committing only if all
// succeed. Effects receive the *sql.Tx (as an any). It does not maintain the due
// index; for a timed definition use SaveStateInTxWithDue so the index
// commits atomically with state and effects.
func (s *PostgresStorage) SaveStateInTx(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, effects ...workflow.TxSideEffect) (int64, error) {
	return saveStateInTx(ctx, s.db, effects, func(tx *sql.Tx) (int64, error) {
		return s.saveState(ctx, tx, id, marking, ctxData, expectedVersion, nil, false)
	})
}

// SaveStateWithDue implements workflow.DueStorage: it saves the
// versioned state and records the instance's next-due time (nil clears it) in
// the due-index column.
func (s *PostgresStorage) SaveStateWithDue(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time) (int64, error) {
	return s.saveState(ctx, s.db, id, marking, ctxData, expectedVersion, due, true)
}

// SaveStateInTxWithDue implements workflow.TransactionalDueStorage: the
// versioned save, the due-index update, and every side effect run in one
// transaction, committing only if all succeed.
func (s *PostgresStorage) SaveStateInTxWithDue(ctx context.Context, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time, effects ...workflow.TxSideEffect) (int64, error) {
	return saveStateInTx(ctx, s.db, effects, func(tx *sql.Tx) (int64, error) {
		return s.saveState(ctx, tx, id, marking, ctxData, expectedVersion, due, true)
	})
}

func (s *PostgresStorage) saveState(ctx context.Context, q querier, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time, setDue bool) (int64, error) {
	stateJSON, err := json.Marshal(marking)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal state: %w", err)
	}
	customCols, customVals := s.customColumns(ctxData, encodeValuePg)
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
		customVals = append(customVals, dueValuePg(due))
	}

	if expectedVersion <= 0 {
		columns := append([]string{s.idColumn, s.stateColumn, s.versionColumn}, customCols...)
		values := append([]any{id, string(stateJSON), int64(1)}, customVals...)
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO NOTHING;",
			s.table, strings.Join(columns, ", "), strings.Join(pgPlaceholders(len(values)), ", "), s.idColumn)

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

	setClauses := []string{
		fmt.Sprintf("%s = $1", s.stateColumn),
		fmt.Sprintf("%s = %s + 1", s.versionColumn, s.versionColumn),
	}
	args := []any{string(stateJSON)}
	n := 1
	for i, col := range customCols {
		n++
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", col, n))
		args = append(args, customVals[i])
	}
	idPlaceholder := n + 1
	versionPlaceholder := n + 2
	args = append(args, id, expectedVersion)

	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s = $%d AND %s = $%d;",
		s.table, strings.Join(setClauses, ", "), s.idColumn, idPlaceholder, s.versionColumn, versionPlaceholder)

	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if affected == 0 {
		return 0, fmt.Errorf("%w: workflow %s (expected version %d)", workflow.ErrConflict, id, expectedVersion)
	}
	return expectedVersion + 1, nil
}

// pgPlaceholders returns ["$1", "$2", ... "$n"].
func pgPlaceholders(n int) []string {
	ph := make([]string, n)
	for i := range ph {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	return ph
}

// encodeValuePg converts a context value into a form PostgreSQL can store.
// Unlike SQLite, PostgreSQL has a native boolean, so bools are stored as-is;
// slices and maps are JSON-encoded.
func encodeValuePg(val any, present bool) any {
	if !present || val == nil {
		return nil
	}
	switch value := val.(type) {
	case []string, []any, map[string]any:
		if jsonBytes, err := json.Marshal(value); err == nil {
			return string(jsonBytes)
		}
		return nil
	default:
		return val
	}
}

// decodeValue reverses encodeValuePg for values read back from PostgreSQL: JSON
// strings become their slice/map values, everything else passes through.
func decodeValue(val any) any {
	strVal, ok := val.(string)
	if !ok || strVal == "" {
		return val
	}
	var jsonArray []any
	if err := json.Unmarshal([]byte(strVal), &jsonArray); err == nil {
		stringArray := make([]string, 0, len(jsonArray))
		allStrings := true
		for _, item := range jsonArray {
			str, ok := item.(string)
			if !ok {
				allStrings = false
				break
			}
			stringArray = append(stringArray, str)
		}
		if allStrings && len(stringArray) > 0 {
			return stringArray
		}
		return jsonArray
	}
	var jsonMap map[string]any
	if err := json.Unmarshal([]byte(strVal), &jsonMap); err == nil {
		return jsonMap
	}
	return strVal
}
