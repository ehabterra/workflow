// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
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

// Email is a post-commit notification, produced by the declared `email`
// after-commit effect.
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

	a := &App{db: db, def: def, hier: hier, dir: dir}
	reg, err := a.registerEffects()
	if err != nil {
		return nil, fmt.Errorf("register effects: %w", err)
	}
	// Fail at startup on an effect the definition names but nothing implements,
	// rather than the first time a rare branch fires.
	if err := reg.Validate(def); err != nil {
		return nil, fmt.Errorf("effect wiring: %w", err)
	}
	a.mgr = workflow.NewManager(workflow.NewRegistry(), store, workflow.WithEffectRegistry(reg))
	return a, nil
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

// fire applies one transition under Execute.
//
// Compare with the pre-#36 version: it took a setup func, an apply func, an
// effects-builder keyed on which branch fired, and a post-commit builder —
// because every one of those was the host's job. Effects and the post-commit
// phase are declared now, so all that remains is seeding the context the guards
// and effects read.
func (a *App) fire(ctx context.Context, id string, seed map[string]any, apply func(*workflow.Workflow) error) error {
	return a.mgr.Execute(ctx, id, a.def, func(wf *workflow.Workflow) error {
		// Execute retries the whole cycle on ErrConflict, so seeding must be
		// idempotent — it is, being a plain overwrite per attempt.
		for k, v := range seed {
			wf.SetContext(k, v)
		}
		return apply(wf)
	})
}

// Submit fires the submit transition after evaluating the ready-gate.
func (a *App) Submit(ctx context.Context, id, actor string) error {
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	ready := readyGate(req)
	err = a.fire(ctx, id, map[string]any{
		"ready":     ready,
		"actor":     actor,
		"submitter": req.Submitter,
		"amount":    req.Amount,
	}, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "submit")
	})
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

// Approve is the core of the spike, and since #34 it is mostly the ladder.
//
// What it no longer does: read the ledger, work out whether the chain would be
// satisfied if this approval were recorded, or check that the actor is a
// required approver. The net answers all three — the approvals are tokens, and
// the join counts them.
//
// What it still does is #35: `sod_ok` is a boolean computed OUTSIDE the
// transaction, because a guard cannot query the record.
func (a *App) Approve(ctx context.Context, id, actor string) error {
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	chain := a.hier.ChainFor(req.Amount)
	lastResort := lastResortAllowed(a.dir, actor, chain)
	sodOk := actor != req.Submitter || lastResort
	role := a.dir.RoleOf(actor)

	err = a.fire(ctx, id, map[string]any{
		"sod_ok":      sodOk,
		"chain":       chain,
		"actor":       actor,
		"role":        role,
		"submitter":   req.Submitter,
		"amount":      req.Amount,
		"last_resort": lastResort,
	}, func(wf *workflow.Workflow) error {
		// The approval IS a token. Appending it and firing happen in one atomic
		// cycle, so an approval the net refuses is never persisted — the
		// authorization hole a reviewer found in the first draft of this spike
		// cannot exist in this shape.
		if _, err := wf.CreateToken("approvals", workflow.TokenData{
			"role": role, "by": actor, "last_resort": lastResort,
		}); err != nil {
			return err
		}
		// The branch is chosen by enablement and guards; its effects follow from
		// the definition. No host-side switch, and no host-side satisfaction test.
		_, err := wf.ApplyAny(ctx, "approve_last_resort", "approve_final", "approve_partial")
		return err
	})
	if err != nil {
		// The library reports only THAT the net refused, so the host re-derives
		// which rule it was to choose a status code. That is #38 — and it is now
		// the only reason these values are recomputed here; nothing about the
		// decision itself depends on them any more.
		if !sodOk || (!lastResort && !slices.Contains(chain, role)) {
			return ErrForbidden
		}
		return classify(err)
	}
	return nil
}

// Reject fires reject.
func (a *App) Reject(ctx context.Context, id, actor string) error {
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	return classify(a.fire(ctx, id, map[string]any{
		"actor": actor, "submitter": req.Submitter, "amount": req.Amount,
	}, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "reject")
	}))
}

// Resubmit returns a rejected requisition to draft.
func (a *App) Resubmit(ctx context.Context, id, actor string) error {
	return classify(a.fire(ctx, id, map[string]any{"actor": actor},
		func(wf *workflow.Workflow) error {
			return wf.ApplyTransitionWithContext(ctx, "resubmit")
		}))
}

// Revoke supersedes an approved requisition deliberately.
func (a *App) Revoke(ctx context.Context, id, actor string) error {
	req, err := loadRequisition(ctx, a.db, id)
	if err != nil {
		return err
	}
	return classify(a.fire(ctx, id, map[string]any{
		"actor": actor, "amount": req.Amount,
	}, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "revoke")
	}))
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

// Ledger returns the approvals recorded so far. Since #34 the ledger is the
// marking — one colored token per approval in the `approvals` pool — so there
// is no ledger table to read and no effect that writes one.
func (a *App) Ledger(ctx context.Context, id string) ([]workflow.Token, error) {
	wf, err := a.mgr.LoadWorkflow(ctx, id, a.def)
	if err != nil {
		return nil, err
	}
	return wf.GetTokens("approvals"), nil
}
