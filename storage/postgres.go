package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/ehabterra/workflow"
)

// PostgresStorage is a PostgreSQL-backed implementation of workflow.Storage and
// workflow.VersionedStorage. It mirrors SQLiteStorage but uses PostgreSQL syntax
// ($N placeholders and INSERT ... ON CONFLICT upserts).
//
// Use it with any database/sql driver that speaks PostgreSQL; the pgx stdlib
// adapter is recommended:
//
//	import _ "github.com/jackc/pgx/v5/stdlib"
//	db, _ := sql.Open("pgx", dsn)
//	store, _ := storage.NewPostgresStorage(db)
//	_ = storage.Initialize(db, store.GenerateSchema())
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
	for _, colDef := range s.customFields {
		columns = append(columns, colDef)
	}
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s);", s.table, strings.Join(columns, ", "))
}

// SaveState upserts the workflow's places and custom fields (last write wins).
func (s *PostgresStorage) SaveState(ctx context.Context, id string, places []workflow.Place, ctxData map[string]any) error {
	return s.saveState(ctx, s.db, id, places, ctxData)
}

// SaveStateTx behaves like SaveState but writes through the provided transaction.
func (s *PostgresStorage) SaveStateTx(ctx context.Context, tx *sql.Tx, id string, places []workflow.Place, ctxData map[string]any) error {
	return s.saveState(ctx, tx, id, places, ctxData)
}

func (s *PostgresStorage) saveState(ctx context.Context, q querier, id string, places []workflow.Place, ctxData map[string]any) error {
	stateJSON, err := json.Marshal(places)
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	customCols, customVals := s.customColumns(ctxData, encodeValuePg)
	columns := append([]string{s.idColumn, s.stateColumn}, customCols...)
	values := append([]any{id, string(stateJSON)}, customVals...)

	// On conflict, update every non-id column from the proposed row.
	setClauses := make([]string, 0, len(columns)-1)
	for _, col := range columns[1:] {
		setClauses = append(setClauses, fmt.Sprintf("%s = EXCLUDED.%s", col, col))
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s;",
		s.table,
		strings.Join(columns, ", "),
		strings.Join(pgPlaceholders(len(values)), ", "),
		s.idColumn,
		strings.Join(setClauses, ", "),
	)

	_, err = q.ExecContext(ctx, query, values...)
	return err
}

// LoadState loads the workflow's places and custom fields.
func (s *PostgresStorage) LoadState(ctx context.Context, id string) ([]workflow.Place, map[string]any, error) {
	return s.loadState(ctx, s.db, id)
}

// LoadStateTx behaves like LoadState but reads through the provided transaction.
func (s *PostgresStorage) LoadStateTx(ctx context.Context, tx *sql.Tx, id string) ([]workflow.Place, map[string]any, error) {
	return s.loadState(ctx, tx, id)
}

func (s *PostgresStorage) loadState(ctx context.Context, q querier, id string) ([]workflow.Place, map[string]any, error) {
	columns := []string{s.stateColumn}
	customKeys := make([]string, 0, len(s.customFields))
	for key, colDef := range s.customFields {
		columns = append(columns, firstField(colDef))
		customKeys = append(customKeys, key)
	}

	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1", strings.Join(columns, ", "), s.table, s.idColumn)

	var stateJSON string
	scanArgs := make([]any, len(columns))
	scanArgs[0] = &stateJSON
	customVals := make([]any, len(customKeys))
	for i := range customVals {
		scanArgs[i+1] = &customVals[i]
	}

	if err := q.QueryRowContext(ctx, query, id).Scan(scanArgs...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, fmt.Errorf("%w: id %s", workflow.ErrWorkflowNotFound, id)
		}
		return nil, nil, fmt.Errorf("failed to load state: %w", err)
	}

	var places []workflow.Place
	if err := json.Unmarshal([]byte(stateJSON), &places); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	ctxData := make(map[string]any, len(customKeys))
	for i, key := range customKeys {
		ctxData[key] = decodeValue(customVals[i])
	}
	return places, ctxData, nil
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

// LoadVersionedState implements workflow.VersionedStorage.
func (s *PostgresStorage) LoadVersionedState(ctx context.Context, id string) ([]workflow.Place, map[string]any, int64, error) {
	places, ctxData, err := s.loadState(ctx, s.db, id)
	if err != nil {
		return nil, nil, 0, err
	}
	var version int64
	query := fmt.Sprintf("SELECT %s FROM %s WHERE %s = $1", s.versionColumn, s.table, s.idColumn)
	if err := s.db.QueryRowContext(ctx, query, id).Scan(&version); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to load version: %w", err)
	}
	return places, ctxData, version, nil
}

// SaveVersionedState implements workflow.VersionedStorage.
func (s *PostgresStorage) SaveVersionedState(ctx context.Context, id string, places []workflow.Place, ctxData map[string]any, expectedVersion int64) (int64, error) {
	return s.saveVersionedState(ctx, s.db, id, places, ctxData, expectedVersion)
}

// SaveVersionedStateTx behaves like SaveVersionedState but writes through the
// provided transaction.
func (s *PostgresStorage) SaveVersionedStateTx(ctx context.Context, tx *sql.Tx, id string, places []workflow.Place, ctxData map[string]any, expectedVersion int64) (int64, error) {
	return s.saveVersionedState(ctx, tx, id, places, ctxData, expectedVersion)
}

func (s *PostgresStorage) saveVersionedState(ctx context.Context, q querier, id string, places []workflow.Place, ctxData map[string]any, expectedVersion int64) (int64, error) {
	stateJSON, err := json.Marshal(places)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal state: %w", err)
	}
	customCols, customVals := s.customColumns(ctxData, encodeValuePg)

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
