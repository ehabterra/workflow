// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ehabterra/workflow"
	_ "github.com/mattn/go-sqlite3"
)

// newApp builds an app over a fresh in-memory database.
//
// The directory deliberately leaves `director` unheld so the last-resort path
// is reachable: a requisition above 50k needs a director, nobody is one, and
// without the admin escape hatch it could never be approved.
func newApp(t *testing.T) (*App, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// Serialize: the shared-cache in-memory DB is one database behind one lock.
	db.SetMaxOpenConns(1)

	dir := NewDirectory(map[string]string{
		"sam":   "site_manager",
		"casey": "commercial_manager",
		"ollie": "ceo",
		"rae":   "site_manager",
	}, "admin")

	app, err := New(ctx, db, DefaultHierarchy, dir)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	return app, ctx
}

func costed(id, code string, amount float64) Line {
	return Line{ID: id, CostCode: code, Amount: amount}
}

func mustStatus(t *testing.T, a *App, ctx context.Context, id, want string) {
	t.Helper()
	got, err := a.Status(ctx, id)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
}

func countRows(t *testing.T, a *App, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := a.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// ledgerSize counts the approvals recorded so far. Since #34 the ledger is the
// marking, so this reads the net rather than a table — there is no approvals
// table any more.
func ledgerSize(t *testing.T, a *App, ctx context.Context, id string) int {
	t.Helper()
	toks, err := a.Ledger(ctx, id)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	return len(toks)
}

// TestChainForThresholds pins the ladder: the chain grows with value, which is
// exactly why the number of required approvals is not knowable at definition
// time.
func TestChainForThresholds(t *testing.T) {
	cases := []struct {
		value float64
		want  int
	}{
		{1_000, 1},   // site_manager alone
		{20_000, 2},  // + commercial_manager
		{200_000, 3}, // + director
		{900_000, 4}, // + ceo
	}
	for _, c := range cases {
		if got := len(DefaultHierarchy.ChainFor(c.value)); got != c.want {
			t.Errorf("ChainFor(%v) = %d roles, want %d", c.value, got, c.want)
		}
	}
}

// TestSingleApproverHappyPath: below the first cap, one approval finalises.
func TestSingleApproverHappyPath(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r1", "REQ-1", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "r1", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	mustStatus(t, a, ctx, "r1", "Submitted")

	if err := a.Approve(ctx, "r1", "sam"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	mustStatus(t, a, ctx, "r1", "Approved")

	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM outbox WHERE req_id = 'r1'`); n != 1 {
		t.Errorf("outbox rows = %d, want 1", n)
	}
	if len(a.Sent) == 0 || a.Sent[len(a.Sent)-1].Template != "approved" {
		t.Errorf("expected a post-commit approved email, got %+v", a.Sent)
	}
}

// TestTwoStepChain: above the first cap the first approval does NOT finalise —
// it fires approve_partial (the self-loop) and the requisition stays Submitted
// until the second role signs off.
func TestTwoStepChain(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r2", "REQ-2", "dana", 20_000, []Line{costed("l1", "CC-200", 20_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "r2", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	if err := a.Approve(ctx, "r2", "sam"); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	mustStatus(t, a, ctx, "r2", "Submitted") // still pending
	if n := ledgerSize(t, a, ctx, "r2"); n != 1 {
		t.Errorf("pool after first approval = %d tokens, want 1", n)
	}

	if err := a.Approve(ctx, "r2", "casey"); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	mustStatus(t, a, ctx, "r2", "Approved")
	// The join consumed both approvals into `approved` and the reset arc cleared
	// what was left, so the pool is empty — the ledger did its job and closed.
	if n := ledgerSize(t, a, ctx, "r2"); n != 0 {
		t.Errorf("pool after the chain closed = %d tokens, want 0", n)
	}
}

// TestPartialApprovalIsNotEnoughEvenTwice: the join counts DISTINCT roles, so
// the same approver signing twice does not advance the requisition. Before #34
// this was a de-duplication the host had to implement over its own ledger.
func TestPartialApprovalIsNotEnoughEvenTwice(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "rd", "REQ-RD", "dana", 20_000, []Line{costed("l1", "CC-200", 20_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "rd", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// sam and rae both hold site_manager — two approvals, one role.
	if err := a.Approve(ctx, "rd", "sam"); err != nil {
		t.Fatalf("first approve: %v", err)
	}
	if err := a.Approve(ctx, "rd", "rae"); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	mustStatus(t, a, ctx, "rd", "Submitted")
	if n := ledgerSize(t, a, ctx, "rd"); n != 2 {
		t.Errorf("pool = %d tokens, want 2 (both recorded, one role)", n)
	}

	if err := a.Approve(ctx, "rd", "casey"); err != nil {
		t.Fatalf("third approve: %v", err)
	}
	mustStatus(t, a, ctx, "rd", "Approved")
}

// TestReadyGateBlocksSubmit: a line without a cost code blocks submission, and
// the host has to translate the rejection itself.
func TestReadyGateBlocksSubmit(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r3", "REQ-3", "dana", 500, []Line{costed("l1", "", 500)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := a.Submit(ctx, "r3", "dana")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("submit err = %v, want ErrNotReady", err)
	}
	mustStatus(t, a, ctx, "r3", "Draft")
}

// TestSeparationOfDuties: the submitter cannot approve their own requisition.
func TestSeparationOfDuties(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r4", "REQ-4", "sam", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "r4", "sam"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	err := a.Approve(ctx, "r4", "sam") // sam is both submitter and site_manager
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("approve err = %v, want ErrForbidden", err)
	}
	mustStatus(t, a, ctx, "r4", "Submitted")
}

// TestRolelessActorCannotApprove: an actor holding no role at all must not be
// able to write into the ledger.
//
// Since #34 the NET refuses this — `role in chain` is a guard over a runtime
// value the definition can now reason about — and because the approval token is
// created inside the same atomic cycle as the firing, a refused approval leaves
// nothing behind at all. The host check that remains only picks the status code.
func TestRolelessActorCannotApprove(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "ra", "REQ-RA", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "ra", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Approve(ctx, "ra", "nobody"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("approve err = %v, want ErrForbidden", err)
	}
	if n := ledgerSize(t, a, ctx, "ra"); n != 0 {
		t.Errorf("pool = %d tokens, want 0 — a roleless actor left a trace", n)
	}
	mustStatus(t, a, ctx, "ra", "Submitted")
}

// TestOutOfChainActorCannotApprove: holding a role is not enough — it must be a
// role this requisition's value actually requires. A ceo is not an approver of
// a $1,000 requisition whose chain is [site_manager].
func TestOutOfChainActorCannotApprove(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "rb", "REQ-RB", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "rb", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Approve(ctx, "rb", "ollie"); !errors.Is(err, ErrForbidden) { // ollie is ceo
		t.Fatalf("approve err = %v, want ErrForbidden", err)
	}
	if n := ledgerSize(t, a, ctx, "rb"); n != 0 {
		t.Errorf("pool = %d tokens, want 0", n)
	}
	// The legitimate approver still works.
	if err := a.Approve(ctx, "rb", "sam"); err != nil {
		t.Fatalf("approve by site_manager: %v", err)
	}
	mustStatus(t, a, ctx, "rb", "Approved")
}

// TestOutboxPayloadIsValidJSON pins the payload written by the declared
// `outbox` effect as marshalled rather than formatted, at a magnitude where %v
// would go exponential.
func TestOutboxPayloadIsValidJSON(t *testing.T) {
	a, ctx := newApp(t)
	const amount = 1.5e21 // needs the full chain, so approve as last-resort admin
	if err := a.Create(ctx, "rj", "REQ-RJ", "dana", amount, []Line{costed("l1", "CC-100", amount)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "rj", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Approve(ctx, "rj", "admin"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	var payload string
	if err := a.db.QueryRowContext(ctx,
		`SELECT payload FROM outbox WHERE req_id = 'rj'`).Scan(&payload); err != nil {
		t.Fatalf("outbox: %v", err)
	}
	var got struct {
		Amount float64 `json:"amount"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload %q is not valid JSON: %v", payload, err)
	}
	if got.Amount != amount {
		t.Errorf("amount = %v, want %v", got.Amount, amount)
	}
}

// TestDeclaredEffectsFireInOrder pins that the effects a transition declares
// all run, in one transaction, for the branch that actually fired. Before #36
// this ordering lived in a hand-assembled Go slice per action.
func TestDeclaredEffectsFireInOrder(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "rk", "REQ-RK", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "rk", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Approve(ctx, "rk", "sam"); err != nil {
		t.Fatalf("approve: %v", err)
	}

	// approve_final declares: project_status, supersede_prior, stamp_approver,
	// audit, notify, outbox — all of which must have landed.
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM audit_log WHERE req_id = 'rk'`); n != 2 {
		t.Errorf("audit rows = %d, want 2 (submit + approve)", n)
	}
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM outbox WHERE req_id = 'rk'`); n != 1 {
		t.Errorf("outbox rows = %d, want 1", n)
	}
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM notifications WHERE req_id = 'rk'`); n != 2 {
		t.Errorf("notification rows = %d, want 2 (submitted + approved)", n)
	}
	var approvedBy string
	if err := a.db.QueryRowContext(ctx, `SELECT approved_by FROM requisitions WHERE id = 'rk'`).Scan(&approvedBy); err != nil {
		t.Fatalf("approved_by: %v", err)
	}
	if approvedBy != "sam" {
		t.Errorf("approved_by = %q, want sam", approvedBy)
	}
	mustStatus(t, a, ctx, "rk", "Approved")
}

// TestAdminLastResort: a chain containing an unheld role (director) is
// unapprovable without the escape hatch; admin completes it alone.
func TestAdminLastResort(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r5", "REQ-5", "dana", 200_000, []Line{costed("l1", "CC-900", 200_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "r5", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Approve(ctx, "r5", "admin"); err != nil {
		t.Fatalf("last-resort approve: %v", err)
	}
	mustStatus(t, a, ctx, "r5", "Approved")

	var detail string
	if err := a.db.QueryRowContext(ctx,
		`SELECT detail FROM audit_log WHERE req_id = 'r5' AND action = 'requisition.approve'`).Scan(&detail); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if detail != "last-resort" {
		t.Errorf("audit detail = %q, want %q", detail, "last-resort")
	}
}

// TestAdminSubmitterCannotSelfApprove: the last-resort hatch is not a way around
// separation of duties.
//
// `admin` submits a 200k requisition, whose chain needs a `director` nobody
// holds — so the hatch IS available to them. It must still be refused, because
// they are the submitter. Caught by review on #49: the host used to compute
// `sod_ok` as `actor != submitter || lastResort` and `approve_last_resort`
// carried no `sod_ok` guard, so both halves said yes.
func TestAdminSubmitterCannotSelfApprove(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "sa", "REQ-SA", "admin", 200_000, []Line{costed("l1", "CC-900", 200_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "sa", "admin"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Approve(ctx, "sa", "admin"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("approve err = %v, want ErrForbidden", err)
	}
	mustStatus(t, a, ctx, "sa", "Submitted")
	if n := ledgerSize(t, a, ctx, "sa"); n != 0 {
		t.Errorf("pool = %d tokens, want 0 — a refused approval left a trace", n)
	}

	// A different admin, same requisition, still works: the hatch is intact.
	a.dir.admins["auditor"] = true
	if err := a.Approve(ctx, "sa", "auditor"); err != nil {
		t.Fatalf("last-resort by a non-submitting admin: %v", err)
	}
	mustStatus(t, a, ctx, "sa", "Approved")
}

// TestRejectResubmitCycle exercises the loop back to draft.
func TestRejectResubmitCycle(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r6", "REQ-6", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "r6", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if err := a.Reject(ctx, "r6", "sam"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	mustStatus(t, a, ctx, "r6", "Rejected")
	if err := a.Resubmit(ctx, "r6", "dana"); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	mustStatus(t, a, ctx, "r6", "Draft")
}

// TestIllegalTransition: approving a draft is rejected as not-enabled.
func TestIllegalTransition(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r7", "REQ-7", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Approve(ctx, "r7", "sam"); err == nil {
		t.Fatal("approve on a draft should fail")
	}
	mustStatus(t, a, ctx, "r7", "Draft")
}

// TestEffectsAreAtomic: when an effect fails, the state change rolls back with
// it. This is the library guarantee that DOES hold and is worth keeping.
func TestEffectsAreAtomic(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "r8", "REQ-8", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "r8", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// Drop the audit table so a declared effect fails mid-transaction.
	if _, err := a.db.ExecContext(ctx, `DROP TABLE audit_log`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := a.Approve(ctx, "r8", "sam"); err == nil {
		t.Fatal("approve should fail when an effect fails")
	}
	// State did not advance despite the transition having fired in memory.
	mustStatus(t, a, ctx, "r8", "Submitted")
	places, err := a.Marking(ctx, "r8")
	if err != nil {
		t.Fatalf("marking: %v", err)
	}
	if len(places) != 1 || places[0] != "submitted" {
		t.Errorf("marking = %v, want [submitted]", places)
	}
}

// TestSupersedeCascade_DivergesMarking is the important one.
//
// Approving a second requisition supersedes the first. The library has no
// atomic multi-instance transition, so the cascade is a raw SQL write: the
// prior requisition's STATUS becomes Superseded while its workflow MARKING
// stays on `approved`. The two disagree, permanently, and nothing in the
// library notices.
//
// This test asserts the divergence rather than guarding against it — it is the
// evidence for issue #37, and it should be inverted into a correctness test
// once atomic multi-instance transitions exist.
func TestSupersedeCascade_DivergesMarking(t *testing.T) {
	a, ctx := newApp(t)
	for _, id := range []string{"c1", "c2"} {
		if err := a.Create(ctx, id, "REQ-"+id, "dana", 1_000, []Line{costed("l-"+id, "CC-100", 1_000)}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if err := a.Submit(ctx, id, "dana"); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
	}
	if err := a.Approve(ctx, "c1", "sam"); err != nil {
		t.Fatalf("approve c1: %v", err)
	}
	mustStatus(t, a, ctx, "c1", "Approved")

	// Approving c2 supersedes c1.
	if err := a.Approve(ctx, "c2", "rae"); err != nil {
		t.Fatalf("approve c2: %v", err)
	}
	mustStatus(t, a, ctx, "c2", "Approved")
	mustStatus(t, a, ctx, "c1", "Superseded") // the record says superseded...

	places, err := a.Marking(ctx, "c1")
	if err != nil {
		t.Fatalf("marking c1: %v", err)
	}
	if len(places) != 1 || places[0] != "approved" {
		t.Fatalf("marking of c1 = %v; the divergence this test documents is gone — "+
			"if atomic multi-instance transitions landed, invert this assertion", places)
	}
	t.Logf("DIVERGENCE (expected, documents issue #37): status=Superseded marking=%v", places)
}

// TestGuardRejectionIsNotIdentifiable pins friction 6: two different rules
// reject through the same undifferentiated library error, and only the host's
// own recomputation tells them apart.
func TestGuardRejectionIsNotIdentifiable(t *testing.T) {
	a, ctx := newApp(t)
	// Not ready (missing cost code) AND submitted by the would-be approver.
	if err := a.Create(ctx, "r9", "REQ-9", "sam", 500, []Line{costed("l1", "", 500)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := a.Submit(ctx, "r9", "sam")
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("submit err = %v, want ErrNotReady", err)
	}
	// The library's own error carries no guard identity — the host produced
	// ErrNotReady by re-running readyGate, not by asking the library.
	req, err := loadRequisition(ctx, a.db, "r9")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if readyGate(req) {
		t.Fatal("expected the ready-gate to be the failing rule")
	}
}

// TestStaleChainIsRefusedInTheTransaction closes friction 2.
//
// `Approve` reads the requisition's amount, derives the approval chain from it,
// and only then fires. If the amount changes in between — a revision, a
// correction, a race — the chain it is about to count approvals against is the
// wrong one. Before #35 there was nowhere to notice: the guard had no
// transaction and no way to ask.
//
// `tx_guard: "... && amountOf() == amount"` asks inside the firing transaction
// and refuses. The requisition stays Submitted and the approval leaves no trace.
func TestStaleChainIsRefusedInTheTransaction(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "st", "REQ-ST", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "st", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}

	// At 1,000 the chain is [site_manager], so sam alone would finalise it.
	// Someone raises the requisition to 20,000 — which needs a second role —
	// between the read and the fire.
	if _, err := a.db.ExecContext(ctx, `UPDATE requisitions SET amount = 20000 WHERE id = 'st'`); err != nil {
		t.Fatalf("revise: %v", err)
	}

	// The host still holds the chain it derived from the old amount. Firing on
	// it would approve a 20,000 requisition on one signature.
	err := a.fire(ctx, "st", map[string]any{
		"chain":       a.hier.ChainFor(1_000), // stale: [site_manager]
		"actor":       "sam",
		"role":        "site_manager",
		"submitter":   "dana",
		"amount":      1_000.0, // stale
		"last_resort": false,
	}, func(wf *workflow.Workflow) error {
		if _, err := wf.CreateToken("approvals", workflow.TokenData{"role": "site_manager", "by": "sam"}); err != nil {
			return err
		}
		_, err := wf.ApplyAny(ctx, "approve_last_resort", "approve_final", "approve_partial")
		return err
	})
	if !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("a chain derived from a stale amount must be refused BY THE GUARD, got %v", err)
	}
	mustStatus(t, a, ctx, "st", "Submitted")
	if n := ledgerSize(t, a, ctx, "st"); n != 0 {
		t.Errorf("pool = %d tokens, want 0 — the refused cycle left a trace", n)
	}

	// Re-reading the amount produces the right chain, and the same approval is
	// then legal — as a partial, because 20,000 needs two roles.
	if err := a.Approve(ctx, "st", "sam"); err != nil {
		t.Fatalf("approve on the current amount: %v", err)
	}
	mustStatus(t, a, ctx, "st", "Submitted") // one of two roles
	if err := a.Approve(ctx, "st", "casey"); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	mustStatus(t, a, ctx, "st", "Approved")
}

// TestLastResortAlsoRefusesAStaleChain: the escape hatch is not an exemption.
//
// `last_resort` is itself derived from the chain, which is derived from an
// amount read outside the transaction — so the branch that uses it needs the
// same in-transaction amount check as the other two. Caught by review on #51: it
// had only the separation-of-duties half, so an admin could approve on a chain
// computed from a value that had since moved.
func TestLastResortAlsoRefusesAStaleChain(t *testing.T) {
	a, ctx := newApp(t)
	// 200,000 needs a director, which nobody holds, so admin's hatch is open.
	if err := a.Create(ctx, "lr", "REQ-LR", "dana", 200_000, []Line{costed("l1", "CC-900", 200_000)}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := a.Submit(ctx, "lr", "dana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := a.db.ExecContext(ctx, `UPDATE requisitions SET amount = 1000 WHERE id = 'lr'`); err != nil {
		t.Fatalf("revise: %v", err)
	}

	err := a.fire(ctx, "lr", map[string]any{
		"chain":       a.hier.ChainFor(200_000), // stale
		"actor":       "admin",
		"role":        "",
		"submitter":   "dana",
		"amount":      200_000.0, // stale
		"last_resort": true,
	}, func(wf *workflow.Workflow) error {
		if _, err := wf.CreateToken("approvals", workflow.TokenData{"role": "", "by": "admin"}); err != nil {
			return err
		}
		_, err := wf.ApplyAny(ctx, "approve_last_resort", "approve_final", "approve_partial")
		return err
	})
	if !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("the last-resort branch must refuse a stale amount too, got %v", err)
	}
	mustStatus(t, a, ctx, "lr", "Submitted")

	// On the current amount, 1,000 needs only a site_manager — and the hatch is
	// no longer open, because that role IS held. The ordinary path works.
	if err := a.Approve(ctx, "lr", "sam"); err != nil {
		t.Fatalf("approve on the current amount: %v", err)
	}
	mustStatus(t, a, ctx, "lr", "Approved")
}

// TestSodOkFailsClosed: a separation-of-duties check that cannot read the record
// must REFUSE, not approve.
//
// The first version of this guard exposed `submitterOf()` and let the expression
// write `actor != submitterOf()`. On a query error it returned "" — and no actor
// equals "", so the check passed and an unreadable requisition was approvable.
// Returning a sentinel instead does not help: nothing equals that either. Only a
// function that owns the whole comparison can choose which way a failure falls.
func TestSodOkFailsClosed(t *testing.T) {
	a, ctx := newApp(t)
	if err := a.Create(ctx, "sc", "REQ-SC", "dana", 1_000, []Line{costed("l1", "CC-100", 1_000)}); err != nil {
		t.Fatal(err)
	}

	// Each case opens and closes its own transaction: the test database allows
	// one connection, so holding one open would block everything else.
	sodOk := func(t *testing.T, id, actor string) bool {
		t.Helper()
		wf, err := workflow.NewWorkflow(id, a.def, "draft")
		if err != nil {
			t.Fatal(err)
		}
		wf.SetContext("actor", actor)
		ev := workflow.NewGuardEvent(ctx, a.def.Transition("approve_final"), nil, nil, nil, wf)

		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		return txGuardEnv(ctx, tx, ev)["sodOk"].(func() bool)()
	}

	// No such requisition: the read fails, and the answer must be "no".
	if sodOk(t, "ghost", "dana") {
		t.Error("an unreadable requisition must not satisfy separation of duties")
	}
	if sodOk(t, "sc", "dana") {
		t.Error("the submitter must not satisfy separation of duties")
	}
	if !sodOk(t, "sc", "sam") {
		t.Error("a different actor must satisfy separation of duties")
	}
}
