// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package approval

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

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
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM approvals WHERE req_id = 'r2'`); n != 1 {
		t.Errorf("ledger rows after first approval = %d, want 1", n)
	}

	if err := a.Approve(ctx, "r2", "casey"); err != nil {
		t.Fatalf("second approve: %v", err)
	}
	mustStatus(t, a, ctx, "r2", "Approved")
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM approvals WHERE req_id = 'r2'`); n != 2 {
		t.Errorf("ledger rows after second approval = %d, want 2", n)
	}
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
// able to write into the append-only ledger. The net cannot make this check —
// chain membership is a runtime value — so the host makes it.
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
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM approvals WHERE req_id = 'ra'`); n != 0 {
		t.Errorf("ledger rows = %d, want 0 — a roleless actor wrote to the ledger", n)
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
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM approvals WHERE req_id = 'rb'`); n != 0 {
		t.Errorf("ledger rows = %d, want 0", n)
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

	// approve_final declares: record_approval, project_status, supersede_prior,
	// stamp_approver, audit, notify, outbox — all of which must have landed.
	if n := countRows(t, a, ctx, `SELECT COUNT(*) FROM approvals WHERE req_id = 'rk'`); n != 1 {
		t.Errorf("ledger rows = %d, want 1", n)
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
	// Drop the ledger table so the approval effect fails mid-transaction.
	if _, err := a.db.ExecContext(ctx, `DROP TABLE approvals`); err != nil {
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
