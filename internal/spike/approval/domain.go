// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

// Package approval is the declarative-coverage spike (issue #45): a realistic
// value-escalated approval workflow implemented against ONLY what the library
// ships today, so the fraction of it that ends up declarative can be measured
// rather than estimated. See COVERAGE.md for the measurement and the friction
// log it produced.
//
// The domain is deliberately generic — a purchase requisition approved up a
// role hierarchy by value — and mirrors the shape found in real Go back-office
// applications: a threshold ladder, an append-only approvals ledger,
// separation of duties, an admin last-resort, revision supersession, and
// audit/outbox/notification effects that must commit with the state change.
//
// This file is the HOST side: schema, records, roles, and the approval brain.
// Every line of it is Go that the workflow definition could not absorb.
package approval

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"sort"

	"github.com/ehabterra/workflow"
)

// Role is a position in the approval hierarchy. Roles are ordered by Level:
// the chain for a given value climbs from the lowest level upward until it
// reaches a role whose Cap covers the value.
type Role struct {
	Name  string
	Level int
	// Cap is the highest value this role may approve alone. A zero Cap means
	// unlimited (the top of the ladder).
	Cap float64
}

// Hierarchy is the role ladder, lowest level first.
type Hierarchy []Role

// DefaultHierarchy is the ladder used by the spike and its tests.
var DefaultHierarchy = Hierarchy{
	{Name: "site_manager", Level: 1, Cap: 5_000},
	{Name: "commercial_manager", Level: 2, Cap: 50_000},
	{Name: "director", Level: 3, Cap: 500_000},
	{Name: "ceo", Level: 4, Cap: 0}, // unlimited
}

// ChainFor returns the roles that must all approve a requisition of the given
// value: every role from the bottom of the ladder up to and including the
// first whose cap covers it. This is the "threshold ladder" — the reason the
// number of required approvals is not known until fire time.
func (h Hierarchy) ChainFor(value float64) []string {
	sorted := make(Hierarchy, len(h))
	copy(sorted, h)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Level < sorted[j].Level })

	var chain []string
	for _, r := range sorted {
		chain = append(chain, r.Name)
		if r.Cap == 0 || value <= r.Cap {
			break
		}
	}
	return chain
}

// Directory maps users to the role each holds. A role with no holder is what
// makes the admin last-resort necessary: without it such a chain can never be
// satisfied and the requisition is stuck forever.
type Directory struct {
	roles  map[string]string // user -> role
	admins map[string]bool
}

// NewDirectory builds a directory from a user->role map and a set of admins.
func NewDirectory(roles map[string]string, admins ...string) *Directory {
	d := &Directory{roles: make(map[string]string, len(roles)), admins: make(map[string]bool, len(admins))}
	maps.Copy(d.roles, roles)
	for _, a := range admins {
		d.admins[a] = true
	}
	return d
}

// RoleOf returns the role held by user, or "" if none.
func (d *Directory) RoleOf(user string) string { return d.roles[user] }

// IsAdmin reports whether user holds the platform admin capability.
func (d *Directory) IsAdmin(user string) bool { return d.admins[user] }

// HasHolder reports whether any user holds role.
func (d *Directory) HasHolder(role string) bool {
	for _, r := range d.roles {
		if r == role {
			return true
		}
	}
	return false
}

// Requisition is the business record. Status duplicates the workflow marking:
// the library persists the marking in its own tables, but every existing
// reader in a real application (list endpoints, task inboxes, reports, SQL
// filters) reads a status column, so it has to be projected back. Keeping the
// two in step is host work today.
type Requisition struct {
	ID         string
	Ref        string
	Submitter  string
	Amount     float64
	Status     string
	ApprovedBy string
	Lines      []Line
}

// Line is one costed line of a requisition. A line with no cost code blocks
// submission (the ready-gate).
type Line struct {
	ID       string
	CostCode string
	Amount   float64
}

// Schema creates the host tables. The library owns its own instance/token
// tables separately; these are the application's.
const Schema = `
CREATE TABLE IF NOT EXISTS requisitions (
	id          TEXT PRIMARY KEY,
	ref         TEXT NOT NULL,
	submitter   TEXT NOT NULL,
	amount      REAL NOT NULL,
	status      TEXT NOT NULL,
	approved_by TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS requisition_lines (
	id        TEXT PRIMARY KEY,
	req_id    TEXT NOT NULL,
	cost_code TEXT NOT NULL DEFAULT '',
	amount    REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_log (
	seq    INTEGER PRIMARY KEY AUTOINCREMENT,
	req_id TEXT NOT NULL,
	action TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	actor  TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS outbox (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	req_id  TEXT NOT NULL,
	event   TEXT NOT NULL,
	payload TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS notifications (
	seq     INTEGER PRIMARY KEY AUTOINCREMENT,
	req_id  TEXT NOT NULL,
	target  TEXT NOT NULL,
	kind    TEXT NOT NULL
);
`

// loadRequisition reads a requisition and its lines.
func loadRequisition(ctx context.Context, q queryer, id string) (Requisition, error) {
	var r Requisition
	err := q.QueryRowContext(ctx,
		`SELECT id, ref, submitter, amount, status, approved_by FROM requisitions WHERE id = ?`, id,
	).Scan(&r.ID, &r.Ref, &r.Submitter, &r.Amount, &r.Status, &r.ApprovedBy)
	if err != nil {
		return Requisition{}, fmt.Errorf("load requisition %s: %w", id, err)
	}
	rows, err := q.QueryContext(ctx, `SELECT id, cost_code, amount FROM requisition_lines WHERE req_id = ? ORDER BY id`, id)
	if err != nil {
		return Requisition{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.CostCode, &l.Amount); err != nil {
			return Requisition{}, err
		}
		r.Lines = append(r.Lines, l)
	}
	return r, rows.Err()
}

// readyGate is the submit precondition: every costed line carries a cost code.
// It is a guard in every meaningful sense, but it cannot BE a guard, because
// evaluating it requires a query and guards have no transaction. The host runs
// it and injects the answer.
func readyGate(r Requisition) bool {
	for _, l := range r.Lines {
		if l.Amount != 0 && l.CostCode == "" {
			return false
		}
	}
	return true
}

// The approvals ledger, the "have all required roles approved?" test, and the
// "is this actor even a required approver?" check all used to live here — some
// 55 lines of Go, including a function that had to SIMULATE the write it was
// about to make, because the transition that records an approval is the one
// whose guard needed the answer.
//
// All of it is gone. Since #34 the approvals are colored tokens in the net's
// `approvals` pool and the definition counts them itself:
//
//	require:
//	  - place: approvals
//	    where: "token.role in chain"
//	    distinct: role
//	    count: "len(chain)"
//
// What remains below is genuine domain code — the ladder that produces the
// chain, and the escape hatch for a chain nobody can satisfy.

// lastResortAllowed reports whether actor may complete the chain alone. It is
// the escape hatch for a chain containing a role nobody holds — without it
// those requisitions are unapprovable.
func lastResortAllowed(d *Directory, actor string, chain []string) bool {
	if !d.IsAdmin(actor) {
		return false
	}
	for _, role := range chain {
		if !d.HasHolder(role) {
			return true
		}
	}
	return false
}

// queryer is the read surface shared by *sql.DB and *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// txGuardEnv is the transaction-scoped guard environment (#35): the functions a
// `tx_guard:` expression may call, each reading the host's own tables THROUGH
// the firing transaction.
//
// This is the half that used to be impossible. Before it, every one of these
// answers had to be computed here, injected into the workflow context, and
// trusted to still be true when the save committed — `ready`, `sod_ok`, and an
// amount the approval chain was derived from. Now the guard asks, at the moment
// it decides, against the state it is about to commit against.
//
// The queries are host SQL and always were. What changed is WHO asks and WHEN.
func txGuardEnv(ctx context.Context, tx any, ev workflow.Event) map[string]any {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok {
		// Impossible with the SQL backends; returning an empty env makes the
		// guard fail loudly rather than silently evaluating against nothing.
		return map[string]any{}
	}
	id := ev.Workflow().Name() // the instance id IS the requisition id

	return map[string]any{
		// readyGate: every costed line carries a cost code. Was `readyGate(req)`
		// in Go, with the answer injected as the `ready` context boolean.
		"readyGate": func() bool {
			var bad int
			if err := sqlTx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM requisition_lines WHERE req_id = ? AND amount != 0 AND cost_code = ''`,
				id).Scan(&bad); err != nil {
				return false
			}
			return bad == 0
		},

		// sodOk: separation of duties, read live. Was the `sod_ok` boolean,
		// computed from a row the host had read before the transaction opened.
		//
		// The COMPARISON is inside the function on purpose. An earlier version
		// exposed `submitterOf()` and let the guard write
		// `actor != submitterOf()` — which failed OPEN: a query error returned
		// "", no actor equals "", so the check passed and an unreadable
		// requisition could be approved. Returning a sentinel instead does not
		// help, for exactly the same reason. Only a function that owns the whole
		// question can decide which way a failure falls.
		"sodOk": func() bool {
			var submitter string
			if err := sqlTx.QueryRowContext(ctx,
				`SELECT submitter FROM requisitions WHERE id = ?`, id).Scan(&submitter); err != nil {
				return false // unreadable record: refuse, do not approve
			}
			actor, _ := ev.Workflow().Context("actor")
			name, _ := actor.(string)
			return name != "" && name != submitter
		},

		// amountOf: the value the approval chain was derived from. The chain
		// itself is still resolved by the host (it is a policy ladder, not a
		// query), so the guard verifies the input it was derived FROM has not
		// moved — a chain computed from a stale amount is the wrong chain.
		"amountOf": func() float64 {
			var amount float64
			if err := sqlTx.QueryRowContext(ctx,
				`SELECT amount FROM requisitions WHERE id = ?`, id).Scan(&amount); err != nil {
				return -1
			}
			return amount
		},
	}
}
