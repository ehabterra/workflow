// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/ehabterra/workflow"
)

// This file is the shared half of marking normalization: instead of one JSON
// blob in the state column, every token is its own row in a child table —
//
//	seq         INTEGER/BIGSERIAL PRIMARY KEY   -- stable scan order
//	workflow_id TEXT NOT NULL                   -- owning instance
//	place       TEXT NOT NULL                   -- where the token rests
//	token_id    TEXT NOT NULL DEFAULT ''        -- '' = uncolored presence token
//	token       TEXT NOT NULL                   -- full token JSON (id/data/enteredAt)
//
// which makes markings queryable ACROSS instances (the shared-pool read-model
// behind workflow.TokenQueryStorage; see docs/roadmap/SHARED_POOL_MODELING.md)
// and keeps a busy pool place from growing one row's blob without bound.
//
// Concurrency stays exactly as before (the "simple flavor"): the whole
// marking is rewritten on every save, guarded by the instance row's version;
// token rows carry no version of their own. Ownership and version
// granularity, not byte layout, set the contention ceiling — the per-token
// delta flavor is deliberately deferred until measurement demands it.
//
// Compatibility is self-healing in both directions: a save through the token
// table blanks the state column, so a NON-empty state blob always marks a
// legacy (or downgraded-writer) row whose blob is authoritative; the next
// save normalizes it. BackfillTokenStates migrates a whole table eagerly so
// the read-model sees instances that have not saved since the upgrade.

// tokenRow is one marking token flattened for insertion.
type tokenRow struct {
	place   workflow.Place
	tokenID string
	json    string
}

// tokenRowsOf flattens a marking into insert rows in deterministic
// (sorted-place, in-place order) so writes and dumps are stable.
func tokenRowsOf(marking workflow.Marking) ([]tokenRow, error) {
	all := marking.AllTokens()
	places := make([]workflow.Place, 0, len(all))
	for p, toks := range all {
		if len(toks) > 0 {
			places = append(places, p)
		}
	}
	slices.Sort(places)

	var rows []tokenRow
	for _, p := range places {
		for _, tok := range all[p] {
			data, err := json.Marshal(tok)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal token %q at %q: %w", tok.ID(), p, err)
			}
			rows = append(rows, tokenRow{place: p, tokenID: string(tok.ID()), json: string(data)})
		}
	}
	return rows, nil
}

// tokenInsertChunk bounds the rows per INSERT statement: 4 parameters per row
// keeps a chunk well under SQLite's 999-parameter default.
const tokenInsertChunk = 200

// replaceTokens rewrites the instance's token rows to mirror the marking:
// DELETE + chunked bulk INSERT. It MUST run inside the same transaction as
// the version-guarded instance write (the callers guarantee this), so the
// instance row and its token rows can never disagree.
func replaceTokens(ctx context.Context, q querier, table string, pg bool, id string, marking workflow.Marking) error {
	del := fmt.Sprintf("DELETE FROM %s WHERE workflow_id = %s", table, placeholder(pg, 1))
	if _, err := q.ExecContext(ctx, del, id); err != nil {
		return fmt.Errorf("delete token rows: %w", err)
	}

	rows, err := tokenRowsOf(marking)
	if err != nil {
		return err
	}
	for start := 0; start < len(rows); start += tokenInsertChunk {
		chunk := rows[start:min(start+tokenInsertChunk, len(rows))]
		values := make([]string, len(chunk))
		args := make([]any, 0, len(chunk)*4)
		for i, r := range chunk {
			n := i * 4
			values[i] = fmt.Sprintf("(%s, %s, %s, %s)",
				placeholder(pg, n+1), placeholder(pg, n+2), placeholder(pg, n+3), placeholder(pg, n+4))
			args = append(args, id, string(r.place), r.tokenID, r.json)
		}
		ins := fmt.Sprintf("INSERT INTO %s (workflow_id, place, token_id, token) VALUES %s",
			table, strings.Join(values, ", "))
		if _, err := q.ExecContext(ctx, ins, args...); err != nil {
			return fmt.Errorf("insert token rows: %w", err)
		}
	}
	return nil
}

// markingFromTokenJSON rebuilds a marking from token-row JSON payloads.
func markingFromTokenJSON(place []string, tokens []string) (workflow.Marking, error) {
	m := workflow.NewMarking(nil)
	for i, raw := range tokens {
		var tok workflow.Token
		if err := json.Unmarshal([]byte(raw), &tok); err != nil {
			return nil, fmt.Errorf("failed to unmarshal token row: %w", err)
		}
		m.AddToken(workflow.Place(place[i]), tok)
	}
	return m, nil
}

// listPlaceTokens is the shared workflow.TokenQueryStorage implementation:
// every token resting in `place` across all instances, in seq order.
func listPlaceTokens(ctx context.Context, q querier, cfg config, pg bool, place workflow.Place, opts workflow.ListOptions) ([]workflow.PlacedToken, error) {
	if !cfg.tokensEnabled() {
		return nil, fmt.Errorf("token table disabled (empty token table name): %w", errors.ErrUnsupported)
	}
	query := fmt.Sprintf("SELECT workflow_id, token FROM %s WHERE place = %s ORDER BY seq",
		cfg.tokensTable(), placeholder(pg, 1))
	args := []any{string(place)}
	if opts.Limit > 0 {
		args = append(args, opts.Limit)
		query += fmt.Sprintf(" LIMIT %s", placeholder(pg, len(args)))
	} else if opts.Offset > 0 && !pg {
		query += " LIMIT -1" // SQLite requires a LIMIT before OFFSET
	}
	if opts.Offset > 0 {
		args = append(args, opts.Offset)
		query += fmt.Sprintf(" OFFSET %s", placeholder(pg, len(args)))
	}

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list place tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []workflow.PlacedToken
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("scan token row: %w", err)
		}
		var tok workflow.Token
		if err := json.Unmarshal([]byte(raw), &tok); err != nil {
			return nil, fmt.Errorf("failed to unmarshal token row: %w", err)
		}
		out = append(out, workflow.PlacedToken{WorkflowID: id, Place: place, Token: tok})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate token rows: %w", err)
	}
	return out, nil
}

// backfillTokens migrates every legacy row (marking JSON still in the state
// column) into token rows, one instance per transaction: parse the blob,
// write the rows, blank the blob — guarded by the instance version so a
// concurrent writer (whose own save normalizes the row anyway) wins cleanly.
// The version is NOT bumped: the backfill changes the representation, not the
// state, so in-flight optimistic saves stay valid. Returns how many instances
// were migrated. Idempotent; safe to run on every upgrade.
func backfillTokens(ctx context.Context, db *sql.DB, cfg config, pg bool) (int, error) {
	listQuery := fmt.Sprintf("SELECT %s FROM %s WHERE %s != '' ORDER BY %s",
		cfg.idColumn, cfg.table, cfg.stateColumn, cfg.idColumn)
	ids, err := scanIDs(db.QueryContext(ctx, listQuery))
	if err != nil {
		return 0, fmt.Errorf("backfill: %w", err)
	}

	migrated := 0
	for _, id := range ids {
		var skip bool
		err := RunInTx(ctx, db, func(tx *sql.Tx) error {
			skip = false
			var blob string
			var version int64
			sel := fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s = %s",
				cfg.stateColumn, cfg.versionColumn, cfg.table, cfg.idColumn, placeholder(pg, 1))
			if err := tx.QueryRowContext(ctx, sel, id).Scan(&blob, &version); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					skip = true // deleted since the scan
					return nil
				}
				return err
			}
			if blob == "" {
				skip = true // a concurrent save already normalized it
				return nil
			}
			marking, err := workflow.UnmarshalMarkingJSON([]byte(blob))
			if err != nil {
				return fmt.Errorf("instance %s: %w", id, err)
			}
			if err := replaceTokens(ctx, tx, cfg.tokensTable(), pg, id, marking); err != nil {
				return fmt.Errorf("instance %s: %w", id, err)
			}
			upd := fmt.Sprintf("UPDATE %s SET %s = '' WHERE %s = %s AND %s = %s",
				cfg.table, cfg.stateColumn, cfg.idColumn, placeholder(pg, 1), cfg.versionColumn, placeholder(pg, 2))
			res, err := tx.ExecContext(ctx, upd, id, version)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil || n == 0 {
				if err != nil {
					return err
				}
				return errBackfillLost // concurrent writer got there first; roll back
			}
			return nil
		})
		if errors.Is(err, errBackfillLost) {
			continue
		}
		if err != nil {
			return migrated, fmt.Errorf("backfill: %w", err)
		}
		if !skip {
			migrated++
		}
	}
	return migrated, nil
}

// errBackfillLost signals a per-instance backfill transaction lost the version
// race to a concurrent writer — not an error, that writer normalized the row.
var errBackfillLost = errors.New("backfill superseded by a concurrent save")

// placeholder returns the n-th SQL parameter placeholder for the dialect:
// "$n" for Postgres, "?" for SQLite.
func placeholder(pg bool, n int) string {
	if pg {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// tokenTableDDL returns the CREATE statements for the token table and its
// indexes. seqType is the dialect's auto-increment key ("INTEGER PRIMARY KEY"
// on SQLite, "BIGSERIAL PRIMARY KEY" on Postgres).
func tokenTableDDL(table, seqType string) []string {
	return []string{
		fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (seq %s, workflow_id TEXT NOT NULL, place TEXT NOT NULL, token_id TEXT NOT NULL DEFAULT '', token TEXT NOT NULL);",
			table, seqType),
		// The instance index serves load/save (all rows of one workflow); the
		// place index IS the cross-instance read-model (ListPlaceTokens).
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s_wf_idx ON %s (workflow_id);", table, table),
		fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s_place_idx ON %s (place);", table, table),
	}
}
