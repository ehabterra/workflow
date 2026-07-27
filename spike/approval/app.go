// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	_ "embed"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	wfyaml "github.com/ehabterra/workflow/yaml"
)

//go:embed workflow.yaml
var definitionYAML []byte

// Sentinel errors the API layer maps to status codes. The library cannot
// produce this distinction: a guard rejection is not identifiable, so the host
// re-derives which rule failed by recomputing it. See COVERAGE.md, friction 6.
var (
	ErrIllegalTransition = errors.New("approval: illegal transition")    // -> 409
	ErrForbidden         = errors.New("approval: separation of duties")  // -> 403
	ErrNotReady          = errors.New("approval: requisition not ready") // -> 422
)

// Email is a post-commit notification. The library has no post-commit effect
// phase, so the host collects these during the transaction and flushes them
// after Execute returns.
type Email struct {
	To       string
	Template string
	ReqID    string
}

// App is the service layer: the library plus everything the library could not
// absorb.
type App struct {
	db   *sql.DB
	mgr  *workflow.Manager
	def  *workflow.Definition
	hier Hierarchy
	dir  *Directory

	// Sent collects post-commit emails so tests can assert on them.
	Sent []Email
}

// New builds the app, creating both the library's tables and the host's.
func New(ctx context.Context, db *sql.DB, hier Hierarchy, dir *Directory) (*App, error) {
	cfg, err := wfyaml.LoadConfigFromBytes(definitionYAML)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	def, err := wfyaml.NewLoader().LoadDefinition(cfg)
	if err != nil {
		return nil, fmt.Errorf("load definition: %w", err)
	}
	store, err := storage.NewSQLiteStorage(db)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("workflow schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return nil, fmt.Errorf("host schema: %w", err)
	}
	return &App{
		db:   db,
		mgr:  workflow.NewManager(workflow.NewRegistry(), store),
		def:  def,
		hier: hier,
		dir:  dir,
	}, nil
}

// Definition exposes the net for diagramming and tests.
func (a *App) Definition() *workflow.Definition { return a.def }

// Create inserts a requisition and its workflow instance.
func (a *App) Create(ctx context.Context, id, ref, submitter string, amount float64, lines []Line) error {
	if _, err := a.db.ExecContext(ctx,
		`INSERT INTO requisitions (id, ref, submitter, amount, status) VALUES (?, ?, ?, ?, 'Draft')`,
		id, ref, submitter, amount,
	); err != nil {
		return err
	}
	for _, l := range lines {
		if _, err := a.db.ExecContext(ctx,
			`INSERT INTO requisition_lines (id, req_id, cost_code, amount) VALUES (?, ?, ?, ?)`,
			l.ID, id, l.CostCode, l.Amount,
		); err != nil {
			return err
		}
	}
	_, err := a.mgr.CreateWorkflow(ctx, id, a.def, "draft")
	return err
}

// effect is one write that must land in the state-change transaction. The
// library takes a single opaque TxSideEffect, so the host builds and sequences
// this list itself. See COVERAGE.md, friction 3.
type effect func(ctx context.Context, tx *sql.Tx) error

// fire is the shared load-fire-save wrapper every action goes through. It
// exists only because effects, the status projection, and the post-commit
// phase all have to be assembled by hand for every transition.
func (a *App) fire(
	ctx context.Context,
	id string,
	setup func(wf *workflow.Workflow),
	apply func(wf *workflow.Workflow) (string, error),
	effects func(fired string) []effect,
	postCommit func(fired string) []Email,
) (string, error) {
	var fired string
	err := a.mgr.Execute(ctx, id, a.def, func(wf *workflow.Workflow) error {
		// Execute retries the whole cycle on ErrConflict, so setup must be
		// re-applied on every attempt and must be idempotent.
		setup(wf)
		name, err := apply(wf)
		if err != nil {
			return err
		}
		fired = name
		// The status projection is derived here and written in the effect
		// below, because the library has no projection binding.
		return nil
	}, workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
		sqlTx, ok := tx.(*sql.Tx)
		if !ok {
			return fmt.Errorf("unexpected tx type %T", tx)
		}
		for _, e := range effects(fired) {
			if err := e(ctx, sqlTx); err != nil {
				return err
			}
		}
		return nil
	}))
	if err != nil {
		return "", err
	}
	a.Sent = append(a.Sent, postCommit(fired)...)
	return fired, nil
}

// statusOf maps the instance's marking back to the record's status column.
// Place metadata carries the label; a marking with more than one marked place
// would make this lossy, which is why the net stays single-token.
func (a *App) statusOf(wf *workflow.Workflow) string {
	for _, p := range wf.CurrentPlaces() {
		if v, ok := a.def.PlaceMetadata(p, "status"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// project writes the derived status onto the record row inside the tx.
func project(reqID, status string) effect {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE requisitions SET status = ? WHERE id = ?`, status, reqID)
		return err
	}
}

func auditEffect(reqID, action, detail, actor string) effect {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO audit_log (req_id, action, detail, actor) VALUES (?, ?, ?, ?)`,
			reqID, action, detail, actor)
		return err
	}
}

func notifyEffect(reqID, target, kind string) effect {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO notifications (req_id, target, kind) VALUES (?, ?, ?)`, reqID, target, kind)
		return err
	}
}

func outboxEffect(reqID, event, payload string) effect {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO outbox (req_id, event, payload) VALUES (?, ?, ?)`, reqID, event, payload)
		return err
	}
}

func ledgerEffect(reqID, actor, role string, lastResort bool) effect {
	return func(ctx context.Context, tx *sql.Tx) error {
		lr := 0
		if lastResort {
			lr = 1
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO approvals (req_id, actor, role, last_resort) VALUES (?, ?, ?, ?)`,
			reqID, actor, role, lr)
		return err
	}
}

// Submit fires the submit transition after evaluating the ready-gate.
func (a *App) Submit(ctx context.Context, id, actor string) error {
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	ready := readyGate(req)
	status := ""
	_, err = a.fire(ctx, id,
		func(wf *workflow.Workflow) { wf.SetContext("ready", ready) },
		func(wf *workflow.Workflow) (string, error) {
			if err := wf.ApplyTransitionWithContext(ctx, "submit"); err != nil {
				return "", err
			}
			status = a.statusOf(wf)
			return "submit", nil
		},
		func(string) []effect {
			return []effect{
				project(id, status),
				auditEffect(id, "requisition.submit", req.Ref, actor),
				notifyEffect(id, "approvers", "submitted"),
			}
		},
		func(string) []Email { return []Email{{To: "approvers", Template: "submitted", ReqID: id}} },
	)
	if err != nil {
		// The library cannot say WHICH guard rejected, so the host recomputes
		// the gate to produce the right error. See COVERAGE.md, friction 6.
		if !ready {
			return ErrNotReady
		}
		return classify(err)
	}
	return nil
}

// Approve is the core of the spike. It shows every friction at once.
func (a *App) Approve(ctx context.Context, id, actor string) error {
	// ---- Phase 1: everything below happens OUTSIDE the transaction, because
	// guards cannot query. The values computed here are already potentially
	// stale by the time the guard reads them. See COVERAGE.md, friction 2.
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	chain := a.hier.ChainFor(req.Amount)
	lastResort := lastResortAllowed(a.dir, actor, chain)
	sodOk := actor != req.Submitter || lastResort

	role := a.dir.RoleOf(actor)
	satisfied, err := chainSatisfied(ctx, a.db, id, chain, role, lastResort)
	if err != nil {
		return err
	}

	status := ""
	fired, err := a.fire(ctx, id,
		func(wf *workflow.Workflow) {
			wf.SetContext("sod_ok", sodOk)
			wf.SetContext("chain_satisfied", satisfied)
		},
		func(wf *workflow.Workflow) (string, error) {
			name, err := wf.ApplyAny(ctx, "approve_final", "approve_partial")
			if err != nil {
				return "", err
			}
			status = a.statusOf(wf)
			return name, nil
		},
		func(fired string) []effect {
			// ---- Effects differ per branch, and the branch is only known
			// here, in Go. This switch is what a per-transition effect binding
			// would replace. See COVERAGE.md, friction 3.
			base := []effect{
				ledgerEffect(id, actor, role, lastResort),
				project(id, status),
			}
			if fired == "approve_partial" {
				return append(base,
					auditEffect(id, "requisition.approve", "pending", actor),
					notifyEffect(id, "approvers", "approval_pending"),
				)
			}
			return append(base,
				supersedePriorEffect(id),
				auditEffect(id, "requisition.approve", detailFor(lastResort), actor),
				notifyEffect(id, req.Submitter, "approved"),
				outboxEffect(id, "requisition.approved", fmt.Sprintf(`{"amount":%v}`, req.Amount)),
				func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `UPDATE requisitions SET approved_by = ? WHERE id = ?`, actor, id)
					return err
				},
			)
		},
		func(fired string) []Email {
			if fired == "approve_final" {
				return []Email{{To: req.Submitter, Template: "approved", ReqID: id}}
			}
			return nil
		},
	)
	if err != nil {
		if !sodOk {
			return ErrForbidden
		}
		return classify(err)
	}
	_ = fired
	return nil
}

// supersedePriorEffect marks every OTHER approved requisition superseded.
//
// This is a raw SQL write, and that is the point: the library has no atomic
// multi-instance transition, so the prior requisitions' STATUS changes while
// their workflow MARKINGS stay on `approved`. State and marking diverge, and
// nothing in the library detects it. TestSupersedeCascade_DivergesMarking
// proves the divergence. See COVERAGE.md, friction 5.
func supersedePriorEffect(newID string) effect {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE requisitions SET status = 'Superseded' WHERE status = 'Approved' AND id != ?`, newID)
		return err
	}
}

func detailFor(lastResort bool) string {
	if lastResort {
		return "last-resort"
	}
	return "chain-satisfied"
}

// Reject fires reject.
func (a *App) Reject(ctx context.Context, id, actor string) error {
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	status := ""
	_, err = a.fire(ctx, id,
		func(*workflow.Workflow) {},
		func(wf *workflow.Workflow) (string, error) {
			if err := wf.ApplyTransitionWithContext(ctx, "reject"); err != nil {
				return "", err
			}
			status = a.statusOf(wf)
			return "reject", nil
		},
		func(string) []effect {
			return []effect{
				project(id, status),
				auditEffect(id, "requisition.reject", req.Ref, actor),
				notifyEffect(id, req.Submitter, "rejected"),
			}
		},
		func(string) []Email { return []Email{{To: req.Submitter, Template: "rejected", ReqID: id}} },
	)
	return classify(err)
}

// Resubmit returns a rejected requisition to draft.
func (a *App) Resubmit(ctx context.Context, id, actor string) error {
	status := ""
	_, err := a.fire(ctx, id,
		func(*workflow.Workflow) {},
		func(wf *workflow.Workflow) (string, error) {
			if err := wf.ApplyTransitionWithContext(ctx, "resubmit"); err != nil {
				return "", err
			}
			status = a.statusOf(wf)
			return "resubmit", nil
		},
		func(string) []effect {
			return []effect{project(id, status), auditEffect(id, "requisition.resubmit", "", actor)}
		},
		func(string) []Email { return nil },
	)
	return classify(err)
}

// Revoke supersedes an approved requisition deliberately.
func (a *App) Revoke(ctx context.Context, id, actor string) error {
	status := ""
	_, err := a.fire(ctx, id,
		func(*workflow.Workflow) {},
		func(wf *workflow.Workflow) (string, error) {
			if err := wf.ApplyTransitionWithContext(ctx, "revoke"); err != nil {
				return "", err
			}
			status = a.statusOf(wf)
			return "revoke", nil
		},
		func(string) []effect {
			return []effect{
				project(id, status),
				auditEffect(id, "requisition.revoke", "", actor),
				outboxEffect(id, "requisition.revoked", `{"reversal":true}`),
			}
		},
		func(string) []Email { return nil },
	)
	return classify(err)
}

// classify maps a library error onto the host's sentinels. Everything that is
// not a recognised library condition collapses to ErrIllegalTransition,
// because the library does not report which guard rejected.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "not enabled") || errors.Is(err, workflow.ErrTransitionNotAllowed) {
		return ErrIllegalTransition
	}
	return err
}

// Status reads the projected status column.
func (a *App) Status(ctx context.Context, id string) (string, error) {
	var s string
	err := a.db.QueryRowContext(ctx, `SELECT status FROM requisitions WHERE id = ?`, id).Scan(&s)
	return s, err
}

// Marking reads the library's own view of the instance, for tests that need to
// compare it against the projected status.
func (a *App) Marking(ctx context.Context, id string) ([]workflow.Place, error) {
	wf, err := a.mgr.LoadWorkflow(ctx, id, a.def)
	if err != nil {
		return nil, err
	}
	return wf.CurrentPlaces(), nil
}
