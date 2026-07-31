// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ehabterra/workflow"
)

// Transaction-scoped cycles (workflow.TxScopedStorage).
//
// The ordinary write path fires a transition in memory and opens a transaction
// only to save. That leaves nowhere for a guard to read from: anything it needs
// must have been resolved before the transaction existed, and may be stale by
// the time it commits. These methods let the Manager borrow a transaction and
// run the WHOLE load → fire → save cycle inside it, so a transaction-scoped
// guard (workflow.NewTxExpressionConstraint) can query as of the state the save
// is about to be checked against.
//
// They are thin: BeginScope is RunInTx, and the load/save are the existing
// *sql.Tx methods behind an `any` so the Manager stays backend-agnostic.

// scopeTx recovers the concrete transaction the Manager is handing back. It can
// only fail if a caller passes a handle from a different backend, which is a
// programming error worth naming precisely rather than panicking on.
func scopeTx(tx any) (*sql.Tx, error) {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok {
		return nil, fmt.Errorf("storage: transaction-scoped call needs a *sql.Tx, got %T", tx)
	}
	if sqlTx == nil {
		return nil, fmt.Errorf("storage: transaction-scoped call needs a non-nil *sql.Tx")
	}
	return sqlTx, nil
}

// --- SQLite ---

// BeginScope implements workflow.TxScopedStorage: it opens a transaction, hands
// it to fn as an any, and commits only if fn returns nil (rolling back on error
// or panic).
func (s *SQLiteStorage) BeginScope(ctx context.Context, fn func(context.Context, any) error) error {
	return RunInTx(ctx, s.db, func(tx *sql.Tx) error {
		return fn(ctx, tx)
	})
}

// LoadStateScoped implements workflow.TxScopedStorage: LoadState through the
// caller's transaction.
func (s *SQLiteStorage) LoadStateScoped(ctx context.Context, tx any, id string) (workflow.Marking, map[string]any, int64, error) {
	sqlTx, err := scopeTx(tx)
	if err != nil {
		return nil, nil, 0, err
	}
	return s.LoadStateTx(ctx, sqlTx, id)
}

// SaveStateScoped implements workflow.TxScopedStorage: the version-guarded save
// through the caller's transaction. It does not touch the due index — see
// SaveStateScopedWithDue.
func (s *SQLiteStorage) SaveStateScoped(ctx context.Context, tx any, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64) (int64, error) {
	sqlTx, err := scopeTx(tx)
	if err != nil {
		return 0, err
	}
	return s.saveState(ctx, sqlTx, id, marking, ctxData, expectedVersion, nil, false)
}

// SaveStateScopedWithDue implements workflow.TxScopedDueStorage: the save and
// the due-index update commit with everything else in the caller's transaction.
func (s *SQLiteStorage) SaveStateScopedWithDue(ctx context.Context, tx any, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time) (int64, error) {
	sqlTx, err := scopeTx(tx)
	if err != nil {
		return 0, err
	}
	return s.saveState(ctx, sqlTx, id, marking, ctxData, expectedVersion, due, true)
}

// --- Postgres ---

// BeginScope implements workflow.TxScopedStorage (see the SQLite method).
func (s *PostgresStorage) BeginScope(ctx context.Context, fn func(context.Context, any) error) error {
	return RunInTx(ctx, s.db, func(tx *sql.Tx) error {
		return fn(ctx, tx)
	})
}

// LoadStateScoped implements workflow.TxScopedStorage: LoadState through the
// caller's transaction.
func (s *PostgresStorage) LoadStateScoped(ctx context.Context, tx any, id string) (workflow.Marking, map[string]any, int64, error) {
	sqlTx, err := scopeTx(tx)
	if err != nil {
		return nil, nil, 0, err
	}
	return s.LoadStateTx(ctx, sqlTx, id)
}

// SaveStateScoped implements workflow.TxScopedStorage: the version-guarded save
// through the caller's transaction.
func (s *PostgresStorage) SaveStateScoped(ctx context.Context, tx any, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64) (int64, error) {
	sqlTx, err := scopeTx(tx)
	if err != nil {
		return 0, err
	}
	return s.saveState(ctx, sqlTx, id, marking, ctxData, expectedVersion, nil, false)
}

// SaveStateScopedWithDue implements workflow.TxScopedDueStorage.
func (s *PostgresStorage) SaveStateScopedWithDue(ctx context.Context, tx any, id string, marking workflow.Marking, ctxData map[string]any, expectedVersion int64, due *time.Time) (int64, error) {
	sqlTx, err := scopeTx(tx)
	if err != nil {
		return 0, err
	}
	return s.saveState(ctx, sqlTx, id, marking, ctxData, expectedVersion, due, true)
}

// Compile-time proof that both backends satisfy the scoped contracts.
var (
	_ workflow.TxScopedDueStorage = (*SQLiteStorage)(nil)
	_ workflow.TxScopedDueStorage = (*PostgresStorage)(nil)
)
