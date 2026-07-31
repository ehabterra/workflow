// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

// Package featuretour is a runnable tour of every feature the library ships.
//
// It exists to be UPDATED WITH EVERY FEATURE. Unlike the other examples, which
// each illustrate one idea, this one is the index: if a user-visible capability
// is not exercised here, either it is undocumented or it has been quietly
// broken. See README.md for the checklist a feature PR follows.
//
// It deliberately lives in the ROOT module rather than having its own go.mod, so
// `go test -p 1 ./...` compiles and runs it against the working tree. An example
// pinned to a published version cannot demonstrate an unreleased feature, and an
// example that does not run cannot fail.
package featuretour

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "embed"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/storage"
	wfyaml "github.com/ehabterra/workflow/yaml"
)

//go:embed workflow.yaml
var definitionYAML []byte

// Schema is the HOST's table. The library owns its own; this one exists so the
// tour has real application state for a transaction-scoped guard to query.
const Schema = `
CREATE TABLE IF NOT EXISTS documents (
	id      TEXT PRIMARY KEY,
	author  TEXT NOT NULL,
	costed  INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS audit_log (
	seq    INTEGER PRIMARY KEY AUTOINCREMENT,
	doc    TEXT NOT NULL,
	action TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS outbox (
	seq   INTEGER PRIMARY KEY AUTOINCREMENT,
	doc   TEXT NOT NULL,
	event TEXT NOT NULL
);
`

// Tour holds everything one run needs.
type Tour struct {
	DB  *sql.DB
	Def *workflow.Definition
	Mgr *workflow.Manager

	// Notified collects after-commit notifications so a caller can assert on
	// them. They are AT-LEAST-ONCE — the library provides the phase, not the
	// guarantee.
	Notified []string

	// now is the tour's clock. Timers are host-driven, so the tour owns time
	// and never sleeps.
	now time.Time
}

// New wires the tour: definition, storage, effect registry, manager.
func New(ctx context.Context, db *sql.DB) (*Tour, error) {
	// The tour owns the clock, but it starts from the real one: tokens are
	// stamped with the workflow's own clock as they enter a place, and Manager
	// does not (yet) let a host inject a clock into an ordinary Execute — only
	// into FireDue. Starting from now and advancing forward keeps the two
	// comparable without sleeping.
	t := &Tour{DB: db, now: time.Now()}

	// [YAML config] Strict decoding: an unknown key is an error with a line
	// number, never a silently ignored typo.
	cfg, err := wfyaml.LoadConfigFromBytes(definitionYAML)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// [tx_guard environment] The functions a tx_guard: expression may call. Each
	// reads through the transaction the firing runs inside — which is the whole
	// point: the answer is as of the state the save will be checked against.
	// What this returns is ADDED to the standard guard environment, so an
	// expression can mix a live read with a value the host passed in.
	def, err := wfyaml.NewLoaderWithTxEnv(func(ctx context.Context, tx any, ev workflow.Event) map[string]any {
		sqlTx, ok := tx.(*sql.Tx)
		if !ok {
			return nil
		}
		id := ev.Workflow().Name()
		return map[string]any{
			"everyLineCosted": func() bool {
				var costed int
				if err := sqlTx.QueryRowContext(ctx,
					`SELECT costed FROM documents WHERE id = ?`, id).Scan(&costed); err != nil {
					return false
				}
				return costed == 1
			},
			"authorOf": func() string {
				var author string
				if err := sqlTx.QueryRowContext(ctx,
					`SELECT author FROM documents WHERE id = ?`, id).Scan(&author); err != nil {
					return ""
				}
				return author
			},
		}
	}).LoadDefinition(cfg)
	if err != nil {
		return nil, fmt.Errorf("load definition: %w", err)
	}
	t.Def = def

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

	// [effect registry] Implementations are registered once at startup; the
	// DEFINITION says which transitions fire which, in what order.
	reg := workflow.NewEffectRegistry()
	if err := reg.Register("audit", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		_, err := tx.(*sql.Tx).ExecContext(ctx,
			`INSERT INTO audit_log (doc, action) VALUES (?, ?)`, ev.WorkflowID, ev.Params["action"])
		return err
	}); err != nil {
		return nil, err
	}
	if err := reg.Register("outbox", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		_, err := tx.(*sql.Tx).ExecContext(ctx,
			`INSERT INTO outbox (doc, event) VALUES (?, ?)`, ev.WorkflowID, ev.Params["event"])
		return err
	}); err != nil {
		return nil, err
	}
	if err := reg.RegisterAfterCommit("notify", func(ctx context.Context, ev workflow.EffectEvent) error {
		t.Notified = append(t.Notified, fmt.Sprintf("%v:%v", ev.Params["to"], ev.Params["template"]))
		return nil
	}); err != nil {
		return nil, err
	}
	// [registry validation] An effect the definition names but nothing
	// implements fails HERE, not the first time a rare branch fires.
	if err := reg.Validate(def); err != nil {
		return nil, fmt.Errorf("effect wiring: %w", err)
	}

	t.Mgr = workflow.NewManager(workflow.NewRegistry(), store, workflow.WithEffectRegistry(reg))
	return t, nil
}

// Now returns the tour's clock. Timers are host-driven: the library answers
// "what is due at T?" and the host decides what T is.
func (t *Tour) Now() time.Time { return t.now }

// Advance moves the tour's clock forward.
func (t *Tour) Advance(d time.Duration) { t.now = t.now.Add(d) }

// CreateDocument inserts the host record and its workflow instance.
func (t *Tour) CreateDocument(ctx context.Context, id, author string, costed bool) error {
	c := 0
	if costed {
		c = 1
	}
	if _, err := t.DB.ExecContext(ctx,
		`INSERT INTO documents (id, author, costed) VALUES (?, ?, ?)`, id, author, c); err != nil {
		return err
	}
	_, err := t.Mgr.CreateWorkflow(ctx, id, t.Def, "draft")
	return err
}

// Fire seeds the instance context and applies transitions through Execute — the
// load → fire → save cycle with optimistic-concurrency retries. Because the
// definition carries a tx_guard, that whole cycle runs inside ONE transaction.
func (t *Tour) Fire(ctx context.Context, id string, seed map[string]any, apply func(*workflow.Workflow) error) error {
	return t.Mgr.Execute(ctx, id, t.Def, func(wf *workflow.Workflow) error {
		// Execute may re-run this on ErrConflict, so seeding must be
		// idempotent — a plain overwrite per attempt is.
		for k, v := range seed {
			wf.SetContext(k, v)
		}
		return apply(wf)
	})
}

// Submit fires the AND-split. Nothing here evaluates the cost-code gate: the
// tx_guard does, inside the transaction.
func (t *Tour) Submit(ctx context.Context, id, actor string) error {
	return t.Fire(ctx, id, map[string]any{"actor": actor}, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "submit")
	})
}

// Sign records one sign-off as a colored token in the pool, and tries to
// approve. The approve transition is simply not ENABLED until the pool holds one
// token per required role, so there is no host-side "are we done?" check.
func (t *Tour) Sign(ctx context.Context, id, actor, role string, requiredRoles []string) (bool, error) {
	roles := make([]any, len(requiredRoles))
	for i, r := range requiredRoles {
		roles[i] = r
	}
	approved := false
	err := t.Fire(ctx, id, map[string]any{
		"actor":          actor,
		"required_roles": roles,
	}, func(wf *workflow.Workflow) error {
		approved = false
		// Appending the sign-off and firing are one atomic cycle: a firing the
		// net refuses persists neither.
		if _, err := wf.CreateToken("signoffs", workflow.TokenData{"role": role, "by": actor}); err != nil {
			return err
		}
		if err := wf.ApplyTransitionWithContext(ctx, "approve"); err != nil {
			// Not enough sign-offs yet is the normal case, not a failure: the
			// token still commits, so the pool accumulates.
			if isNotEnabled(err) {
				return nil
			}
			return err
		}
		approved = true
		return nil
	})
	return approved, err
}

// Reject cancels the sibling branch and the collected sign-offs with one firing.
func (t *Tour) Reject(ctx context.Context, id, actor string, roles []string) error {
	return t.Fire(ctx, id, map[string]any{"actor": actor, "roles": roles}, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "reject")
	})
}

// Archive fires the OR-input transition from whichever stage is marked.
func (t *Tour) Archive(ctx context.Context, id string) error {
	return t.Fire(ctx, id, nil, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "archive")
	})
}

// FireDue advances every instance whose deadline has elapsed as of the tour's
// clock — the host cron, made explicit.
func (t *Tour) FireDue(ctx context.Context, id string) ([]string, error) {
	return t.Mgr.FireDue(ctx, id, t.Def, t.now)
}

// Marking is the library's view of the instance: the set of places holding
// tokens, which IS the state.
func (t *Tour) Marking(ctx context.Context, id string) ([]workflow.Place, error) {
	wf, err := t.Mgr.LoadWorkflow(ctx, id, t.Def)
	if err != nil {
		return nil, err
	}
	return wf.CurrentPlaces(), nil
}

// Status projects the marking onto the host's vocabulary through place
// metadata. The map is still host code (#39).
func (t *Tour) Status(ctx context.Context, id string) (string, error) {
	places, err := t.Marking(ctx, id)
	if err != nil {
		return "", err
	}
	for _, p := range places {
		if v, ok := t.Def.PlaceMetadata(p, "status"); ok {
			if s, ok := v.(string); ok {
				return s, nil
			}
		}
	}
	return "", nil
}

// Signoffs returns the tokens currently in the pool.
func (t *Tour) Signoffs(ctx context.Context, id string) ([]workflow.Token, error) {
	wf, err := t.Mgr.LoadWorkflow(ctx, id, t.Def)
	if err != nil {
		return nil, err
	}
	return wf.GetTokens("signoffs"), nil
}

// Diagram renders the live instance — marking highlighted, guards and joins on
// the nodes. Generated from the same definition the engine fires, so it cannot
// drift from behavior.
func (t *Tour) Diagram(ctx context.Context, id string) (string, error) {
	wf, err := t.Mgr.LoadWorkflow(ctx, id, t.Def)
	if err != nil {
		return "", err
	}
	return wf.Diagram(), nil
}

// isNotEnabled reports the "not yet" condition without treating a guard
// rejection as one.
func isNotEnabled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not enabled")
}
