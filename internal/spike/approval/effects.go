// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/ehabterra/workflow"
)

// registerEffects wires the host implementations the definition names.
//
// This is what #36 moved: the implementations stay (they are host code and
// always were), but which effects a transition fires, in what order, and which
// differ per branch is now DECLARED in workflow.yaml. Before, every one of
// those decisions was Go — a per-action effect list assembled by hand in each
// service method, plus a switch on which branch ApplyAny picked.
//
// Note what these signatures no longer need: no per-call closure capture, no
// `fire` wrapper threading effects through Execute, no post-commit collection.
// The registry is built once at startup and shared.
func (a *App) registerEffects() (*workflow.EffectRegistry, error) {
	reg := workflow.NewEffectRegistry()

	// project_status writes the marking-derived status onto the record row, in
	// the same transaction. Still host code — see #39, which would make this a
	// declared binding rather than an effect the definition has to remember.
	if err := reg.Register("project_status", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		sqlTx, err := asTx(tx)
		if err != nil {
			return err
		}
		_, err = sqlTx.ExecContext(ctx, `UPDATE requisitions SET status = ? WHERE id = ?`,
			a.statusFor(ev.After), ev.WorkflowID)
		return err
	}); err != nil {
		return nil, err
	}

	if err := reg.Register("audit", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		sqlTx, err := asTx(tx)
		if err != nil {
			return err
		}
		// The detail is a declared param throughout — including the last-resort
		// marker, which used to travel in context because the host had to work
		// out which branch it was about to take. The branch declares its own now.
		_, err = sqlTx.ExecContext(ctx,
			`INSERT INTO audit_log (req_id, action, detail, actor) VALUES (?, ?, ?, ?)`,
			ev.WorkflowID, str(ev.Params["action"]), str(ev.Params["detail"]), str(ev.Context["actor"]))
		return err
	}); err != nil {
		return nil, err
	}

	if err := reg.Register("notify", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		sqlTx, err := asTx(tx)
		if err != nil {
			return err
		}
		_, err = sqlTx.ExecContext(ctx,
			`INSERT INTO notifications (req_id, target, kind) VALUES (?, ?, ?)`,
			ev.WorkflowID, a.resolveTarget(ev), str(ev.Params["kind"]))
		return err
	}); err != nil {
		return nil, err
	}

	if err := reg.Register("outbox", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		sqlTx, err := asTx(tx)
		if err != nil {
			return err
		}
		payload, err := json.Marshal(map[string]any{
			"amount":   ev.Context["amount"],
			"reversal": ev.Params["reversal"] == true,
		})
		if err != nil {
			return err
		}
		_, err = sqlTx.ExecContext(ctx,
			`INSERT INTO outbox (req_id, event, payload) VALUES (?, ?, ?)`,
			ev.WorkflowID, str(ev.Params["event"]), string(payload))
		return err
	}); err != nil {
		return nil, err
	}

	// There was a `record_approval` effect here, appending a row to an approvals
	// table. #34 deleted it: an approval is a colored token in the net's pool
	// now, written by the same atomic save as the marking, and the join counts
	// the tokens directly. The prediction under friction 1 was that a
	// dynamic-cardinality join "would make the approval a token and this effect
	// disappear" — it did.

	if err := reg.Register("stamp_approver", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		sqlTx, err := asTx(tx)
		if err != nil {
			return err
		}
		_, err = sqlTx.ExecContext(ctx, `UPDATE requisitions SET approved_by = ? WHERE id = ?`,
			str(ev.Context["actor"]), ev.WorkflowID)
		return err
	}); err != nil {
		return nil, err
	}

	// supersede_prior is STILL a raw SQL cascade, and still wrong: the prior
	// requisitions' status changes while their markings stay on `approved`.
	// #36 declared WHEN it runs; only #37 can make it a real transition.
	// TestSupersedeCascade_DivergesMarking still documents the divergence.
	if err := reg.Register("supersede_prior", func(ctx context.Context, tx any, ev workflow.EffectEvent) error {
		sqlTx, err := asTx(tx)
		if err != nil {
			return err
		}
		_, err = sqlTx.ExecContext(ctx,
			`UPDATE requisitions SET status = 'Superseded' WHERE status = 'Approved' AND id != ?`,
			ev.WorkflowID)
		return err
	}); err != nil {
		return nil, err
	}

	// email is the post-commit phase — deliberately outside the transaction,
	// and at-least-once. Before #36 the host collected these during the fire
	// and flushed them after Execute returned, per action.
	if err := reg.RegisterAfterCommit("email", func(ctx context.Context, ev workflow.EffectEvent) error {
		a.Sent = append(a.Sent, Email{
			To:       a.resolveTarget(ev),
			Template: str(ev.Params["template"]),
			ReqID:    ev.WorkflowID,
		})
		return nil
	}); err != nil {
		return nil, err
	}

	return reg, nil
}

// resolveTarget maps the declared audience to an address. "submitter" is
// per-instance, so it comes from context; anything else is a literal.
func (a *App) resolveTarget(ev workflow.EffectEvent) string {
	target := str(ev.Params["target"])
	if target == "" {
		target = str(ev.Params["to"])
	}
	if target == "submitter" {
		return str(ev.Context["submitter"])
	}
	return target
}

// statusFor maps a marking to the record's status column via place metadata.
func (a *App) statusFor(places []workflow.Place) string {
	for _, p := range places {
		if v, ok := a.def.PlaceMetadata(p, "status"); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func asTx(tx any) (*sql.Tx, error) {
	sqlTx, ok := tx.(*sql.Tx)
	if !ok {
		return nil, fmt.Errorf("unexpected tx type %T", tx)
	}
	return sqlTx, nil
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
