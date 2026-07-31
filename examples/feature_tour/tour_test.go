// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

package featuretour

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	_ "github.com/mattn/go-sqlite3"
)

// Every test below is named for the feature it pins. A feature PR adds one.

func newTour(t *testing.T) (*Tour, context.Context) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1) // one shared in-memory database behind one lock

	tour, err := New(ctx, db)
	if err != nil {
		t.Fatalf("new tour: %v", err)
	}
	return tour, ctx
}

func mustMarking(t *testing.T, tour *Tour, ctx context.Context, id string, want ...workflow.Place) {
	t.Helper()
	got, err := tour.Marking(ctx, id)
	if err != nil {
		t.Fatalf("marking: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("marking = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("marking = %v, want %v", got, want)
		}
	}
}

func count(t *testing.T, tour *Tour, ctx context.Context, query string, args ...any) int {
	t.Helper()
	var n int
	if err := tour.DB.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// TestMarkingIsTheState — the whole mental model. Two places hold tokens at
// once, which no status column can represent.
func TestMarkingIsTheState(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d1", "ana", true); err != nil {
		t.Fatal(err)
	}
	mustMarking(t, tour, ctx, "d1", "draft")

	if err := tour.Submit(ctx, "d1", "ana"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	// [AND-split] Legal AND finance, simultaneously.
	mustMarking(t, tour, ctx, "d1", "finance", "legal")

	// [place metadata → status projection] Cosmetic, and excluded from the
	// fingerprint, so relabelling never invalidates a running instance.
	if s, err := tour.Status(ctx, "d1"); err != nil || s != "In review" {
		t.Fatalf("status = %q (err %v), want %q", s, err, "In review")
	}
}

// TestTxGuardReadsHostStateInTheFiringTransaction — #35. Nothing pre-computes
// the cost-code gate; the guard queries the host table inside the transaction.
func TestTxGuardReadsHostStateInTheFiringTransaction(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d2", "ana", false); err != nil { // not costed
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d2", "ana"); !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("uncosted submit: want ErrGuardRejected, got %v", err)
	}
	mustMarking(t, tour, ctx, "d2", "draft")

	// Fix the host record — no workflow change at all — and it now passes.
	if _, err := tour.DB.ExecContext(ctx, `UPDATE documents SET costed = 1 WHERE id = 'd2'`); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d2", "ana"); err != nil {
		t.Fatalf("costed submit: %v", err)
	}
	mustMarking(t, tour, ctx, "d2", "finance", "legal")
}

// TestDynamicCardinalityJoin — #34. How many sign-offs are needed is a runtime
// value, and the transition is not enabled until the pool satisfies it.
func TestDynamicCardinalityJoin(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d3", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d3", "ana"); err != nil {
		t.Fatal(err)
	}

	required := []string{"legal", "finance"}

	approved, err := tour.Sign(ctx, "d3", "raj", "legal", required)
	if err != nil {
		t.Fatalf("first signoff: %v", err)
	}
	if approved {
		t.Fatal("one of two roles must not approve")
	}

	// [distinct] A second sign-off from the SAME role does not advance it.
	if approved, err = tour.Sign(ctx, "d3", "sam", "legal", required); err != nil {
		t.Fatalf("duplicate role: %v", err)
	} else if approved {
		t.Fatal("two sign-offs from one role are one role")
	}

	if approved, err = tour.Sign(ctx, "d3", "kim", "finance", required); err != nil {
		t.Fatalf("second role: %v", err)
	} else if !approved {
		t.Fatal("the chain is satisfied; approve should have fired")
	}
	mustMarking(t, tour, ctx, "d3", "approved")

	// [reset arc] The leftover legal sign-off went with the firing.
	toks, err := tour.Signoffs(ctx, "d3")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 0 {
		t.Fatalf("pool = %d tokens, want 0", len(toks))
	}
}

// TestSeparationOfDutiesIsReadLive — #35 again, this time as authorization: the
// author cannot approve, checked against the record rather than a passed-in flag.
func TestSeparationOfDutiesIsReadLive(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d4", "raj", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d4", "raj"); err != nil {
		t.Fatal(err)
	}
	required := []string{"legal"}

	// raj is the author, so the approve is refused — and because the sign-off
	// token is created inside the same cycle, it leaves no trace.
	if _, err := tour.Sign(ctx, "d4", "raj", "legal", required); !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("author approving: want ErrGuardRejected, got %v", err)
	}
	toks, err := tour.Signoffs(ctx, "d4")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 0 {
		t.Fatalf("a refused cycle left %d tokens behind", len(toks))
	}

	// Anyone else works.
	if approved, err := tour.Sign(ctx, "d4", "kim", "legal", required); err != nil || !approved {
		t.Fatalf("non-author signoff: approved=%v err=%v", approved, err)
	}
}

// TestResetArcsCancelSiblingBranches — rejecting one branch cancels the other
// and discards collected sign-offs, atomically.
func TestResetArcsCancelSiblingBranches(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d5", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d5", "ana"); err != nil {
		t.Fatal(err)
	}
	if _, err := tour.Sign(ctx, "d5", "kim", "legal", []string{"legal", "finance"}); err != nil {
		t.Fatal(err)
	}

	// [guard] hasRole('legal') over the instance context.
	if err := tour.Reject(ctx, "d5", "kim", []string{"legal"}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	mustMarking(t, tour, ctx, "d5", "rejected")

	toks, err := tour.Signoffs(ctx, "d5")
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 0 {
		t.Fatalf("reset arc left %d sign-offs behind", len(toks))
	}
}

// TestGuardRejectsWithoutTheRole — the ordinary guard path.
func TestGuardRejectsWithoutTheRole(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d6", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d6", "ana"); err != nil {
		t.Fatal(err)
	}
	if err := tour.Reject(ctx, "d6", "kim", []string{"finance"}); !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("want ErrGuardRejected, got %v", err)
	}
}

// TestHostDrivenTimers — the library records when tokens entered a place and
// answers "what is due at T?". It never fires anything on its own.
func TestHostDrivenTimers(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d7", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d7", "ana"); err != nil {
		t.Fatal(err)
	}

	fired, err := tour.FireDue(ctx, "d7")
	if err != nil {
		t.Fatalf("FireDue before the deadline: %v", err)
	}
	if len(fired) != 0 {
		t.Fatalf("nothing is due yet, %v fired", fired)
	}

	tour.Advance(73 * time.Hour)
	fired, err = tour.FireDue(ctx, "d7")
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
	if len(fired) != 1 || fired[0] != "escalate" {
		t.Fatalf("fired = %v, want [escalate]", fired)
	}
	// legal is still in review; finance escalated.
	mustMarking(t, tour, ctx, "d7", "escalated", "legal")
}

// TestOrInputConsumesOnlyTheMarkedStage — from_any.
func TestOrInputConsumesOnlyTheMarkedStage(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d8", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d8", "ana"); err != nil {
		t.Fatal(err)
	}
	tour.Advance(73 * time.Hour)
	if _, err := tour.FireDue(ctx, "d8"); err != nil {
		t.Fatal(err)
	}

	// archive is enabled by `escalated` alone, and consumes only it.
	if err := tour.Archive(ctx, "d8"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	mustMarking(t, tour, ctx, "d8", "archived", "legal")
}

// TestEffectsCommitWithTheStateChange — declared effects run in the state-save
// transaction, in declared order, for the transition that actually fired.
func TestEffectsCommitWithTheStateChange(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d9", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d9", "ana"); err != nil {
		t.Fatal(err)
	}
	if _, err := tour.Sign(ctx, "d9", "kim", "legal", []string{"legal"}); err != nil {
		t.Fatal(err)
	}

	if n := count(t, tour, ctx, `SELECT COUNT(*) FROM audit_log WHERE doc = 'd9'`); n != 2 {
		t.Errorf("audit rows = %d, want 2 (submit + approve)", n)
	}
	// outbox is declared only on approve.
	if n := count(t, tour, ctx, `SELECT COUNT(*) FROM outbox WHERE doc = 'd9'`); n != 1 {
		t.Errorf("outbox rows = %d, want 1", n)
	}
	// [after_commit] Ran outside the transaction, after it committed.
	if len(tour.Notified) != 2 {
		t.Errorf("notifications = %v, want 2 (submitted + approved)", tour.Notified)
	}
}

// TestEffectFailureRollsBackTheStateChange — the guarantee that makes this worth
// building on rather than a bare FSM.
func TestEffectFailureRollsBackTheStateChange(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d10", "ana", true); err != nil {
		t.Fatal(err)
	}
	if _, err := tour.DB.ExecContext(ctx, `DROP TABLE audit_log`); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d10", "ana"); err == nil {
		t.Fatal("submit should fail when its effect fails")
	}
	// The transition fired in memory; nothing persisted.
	mustMarking(t, tour, ctx, "d10", "draft")
}

// TestDefinitionFingerprintCatchesADriftedDefinition — every save stamps a
// structural fingerprint, so an instance cannot silently run against a
// definition it was not created under.
func TestDefinitionFingerprintCatchesADriftedDefinition(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d11", "ana", true); err != nil {
		t.Fatal(err)
	}

	changed, err := workflow.NewDefinition(
		append(tour.Def.AllPlaces(), "extra"), tour.Def.AllTransitions())
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint() == tour.Def.Fingerprint() {
		t.Fatal("fixture: the definitions should differ")
	}
	if _, err := tour.Mgr.LoadWorkflow(ctx, "d11", changed); !errors.Is(err, workflow.ErrDefinitionMismatch) {
		t.Fatalf("want ErrDefinitionMismatch, got %v", err)
	}
}

// TestDiagramIsGeneratedFromTheDefinition — always-accurate diagrams: the same
// definition the engine fires, with the live marking highlighted.
func TestDiagramIsGeneratedFromTheDefinition(t *testing.T) {
	tour, ctx := newTour(t)
	if err := tour.CreateDocument(ctx, "d12", "ana", true); err != nil {
		t.Fatal(err)
	}
	if err := tour.Submit(ctx, "d12", "ana"); err != nil {
		t.Fatal(err)
	}

	diagram, err := tour.Diagram(ctx, "d12")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"flowchart TD",          // it is Mermaid
		"class p_legal current", // the live marking is highlighted
		"⛁",                     // the tx guard is visible
		"signoffs #62;=",        // the dynamic join is visible (Mermaid escapes >)
		"cancels",               // reset arcs are visible
	} {
		if !strings.Contains(diagram, want) {
			t.Errorf("diagram should contain %q:\n%s", want, diagram)
		}
	}
}
