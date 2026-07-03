package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ehabterra/workflow"
)

// querier abstracts the query-execution methods shared by *sql.DB and *sql.Tx,
// so the storage logic can run either directly against the database or inside a
// caller-provided transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// saveVersionedInTx runs a versioned state save and the given side effects in
// one RunInTx transaction, returning the new version. It backs the
// workflow.TransactionalStorage implementations of both SQL backends.
func saveVersionedInTx(ctx context.Context, db *sql.DB, effects []workflow.TxSideEffect, save func(*sql.Tx) (int64, error)) (int64, error) {
	var newVersion int64
	err := RunInTx(ctx, db, func(tx *sql.Tx) error {
		v, err := save(tx)
		if err != nil {
			return err
		}
		newVersion = v
		for _, effect := range effects {
			if err := effect(ctx, tx); err != nil {
				return fmt.Errorf("tx side effect: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return newVersion, nil
}

// scanIDs collects the single-column string results of an ID query, closing the
// rows. It centralizes the boilerplate shared by the SQLite and Postgres
// ListIDs implementations.
func scanIDs(rows *sql.Rows, queryErr error) ([]string, error) {
	if queryErr != nil {
		return nil, fmt.Errorf("list ids: %w", queryErr)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ids: %w", err)
	}
	return ids, nil
}

// RunInTx runs fn inside a database transaction. It commits if fn returns nil and
// rolls back if fn returns an error or panics (the panic is re-raised after the
// rollback). It lets callers persist workflow state and append history atomically,
// so a crash mid-transition cannot leave the state and the audit log disagreeing:
//
//	err := storage.RunInTx(ctx, db, func(tx *sql.Tx) error {
//	    if err := store.SaveStateTx(ctx, tx, id, places, data); err != nil {
//	        return err
//	    }
//	    return hist.SaveTransitionTx(ctx, tx, record)
//	})
//
// The same *sql.DB must back both stores for their writes to share the transaction.
func RunInTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (err error) {
	if db == nil {
		return errors.New("storage: RunInTx requires a non-nil *sql.DB")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("rollback: %w", rbErr))
			}
			return
		}
		if cErr := tx.Commit(); cErr != nil {
			err = fmt.Errorf("commit: %w", cErr)
		}
	}()

	return fn(tx)
}
