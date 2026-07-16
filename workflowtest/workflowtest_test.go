package workflowtest_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/workflowtest"
)

// reviewNet: the dogfood-shaped miniature — submit AND-splits into two
// review lanes, finalize AND-joins them; submit_auto is the guarded fast
// path; escalate is timed.
func reviewNet(t *testing.T) *workflow.Definition {
	t.Helper()
	submit := workflow.MustNewTransition("submit", []workflow.Place{"draft"}, []workflow.Place{"legal", "finance"})
	auto := workflow.MustNewTransition("submit_auto", []workflow.Place{"draft"}, []workflow.Place{"approved"})
	gc, err := workflow.NewExpressionConstraint("getContext('amount', 0.0) <= 100.0")
	if err != nil {
		t.Fatal(err)
	}
	auto.AddConstraint(gc)
	legal := workflow.MustNewTransition("legal_ok", []workflow.Place{"legal"}, []workflow.Place{"legal_done"})
	finance := workflow.MustNewTransition("finance_ok", []workflow.Place{"finance"}, []workflow.Place{"finance_done"})
	finalize := workflow.MustNewTransition("finalize", []workflow.Place{"legal_done", "finance_done"}, []workflow.Place{"approved"})
	escalate := workflow.MustNewTransition("escalate", []workflow.Place{"legal"}, []workflow.Place{"escalated"})
	escalate.SetTimeoutAfter(72 * time.Hour)
	def, err := workflow.NewDefinition(
		[]workflow.Place{"draft", "legal", "finance", "legal_done", "finance_done", "approved", "escalated"},
		[]workflow.Transition{*submit, *auto, *legal, *finance, *finalize, *escalate},
	)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// recordTB captures a helper's Fatalf instead of failing the real test, so
// the failure paths themselves can be asserted. Fatalf must not return —
// helpers rely on it — so it exits the goroutine expectTBFailure runs it in.
type recordTB struct {
	testing.TB
	failed bool
	msg    string
}

func (r *recordTB) Helper() {}
func (r *recordTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// expectTBFailure runs fn against a recording TB and returns the Fatalf
// message, failing t if fn did not fail.
func expectTBFailure(t *testing.T, fn func(tb testing.TB)) string {
	t.Helper()
	rec := &recordTB{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(rec)
	}()
	<-done
	if !rec.failed {
		t.Fatal("expected the helper to fail the test, it passed")
	}
	return rec.msg
}

func TestMarkingAssertions(t *testing.T) {
	def := reviewNet(t)
	wf, err := workflow.NewWorkflow("wf", def, "draft")
	if err != nil {
		t.Fatal(err)
	}

	workflowtest.AssertMarking(t, wf, "draft")
	workflowtest.AssertHas(t, wf, "draft")
	workflowtest.AssertNotHas(t, wf, "approved", "legal")

	msg := expectTBFailure(t, func(tb testing.TB) {
		workflowtest.AssertMarking(tb, wf, "approved")
	})
	if !strings.Contains(msg, "draft") || !strings.Contains(msg, "approved") {
		t.Fatalf("AssertMarking failure %q should name got and want", msg)
	}
	expectTBFailure(t, func(tb testing.TB) { workflowtest.AssertHas(tb, wf, "approved") })
	expectTBFailure(t, func(tb testing.TB) { workflowtest.AssertNotHas(tb, wf, "draft") })
}

func TestApplyPathRunner(t *testing.T) {
	def := reviewNet(t)
	wf, err := workflow.NewWorkflow("wf", def, "draft")
	if err != nil {
		t.Fatal(err)
	}

	// The canonical acceptance path: submit → both approvals → finalize.
	workflowtest.Apply(t, wf, "submit", "legal_ok", "finance_ok", "finalize")
	workflowtest.AssertMarking(t, wf, "approved")

	// A failing step names the step, the transition, and the marking.
	wf2, _ := workflow.NewWorkflow("wf2", def, "draft")
	msg := expectTBFailure(t, func(tb testing.TB) {
		workflowtest.Apply(tb, wf2, "submit", "finalize")
	})
	if !strings.Contains(msg, "step 2/2") || !strings.Contains(msg, `"finalize"`) {
		t.Fatalf("Apply failure %q should name the failing step", msg)
	}
}

func TestGuardHarness(t *testing.T) {
	def := reviewNet(t)

	// Table form: the guard boundary in one assertion, no storage, no Manager.
	workflowtest.AssertGuard(t, def, "submit_auto",
		workflowtest.GuardCase{Name: "petty cash", Context: map[string]any{"amount": 50.0}, Allow: true},
		workflowtest.GuardCase{Name: "at the limit", Context: map[string]any{"amount": 100.0}, Allow: true},
		workflowtest.GuardCase{Name: "over the limit", Context: map[string]any{"amount": 100.01}, Allow: false},
	)

	// Shorthand forms.
	workflowtest.AssertGuardAllows(t, def, "submit_auto", map[string]any{"amount": 10.0})
	workflowtest.AssertGuardRejects(t, def, "submit_auto", map[string]any{"amount": 5000.0})

	// A wrong expectation fails with the case name.
	msg := expectTBFailure(t, func(tb testing.TB) {
		workflowtest.AssertGuard(tb, def, "submit_auto",
			workflowtest.GuardCase{Name: "should-fail", Context: map[string]any{"amount": 5000.0}, Allow: true})
	})
	if !strings.Contains(msg, "should-fail") {
		t.Fatalf("failure %q should carry the case name", msg)
	}

	// Unknown transition fails loudly.
	expectTBFailure(t, func(tb testing.TB) {
		workflowtest.AssertGuard(tb, def, "nope")
	})
}

func TestGuardHarnessWithTokens(t *testing.T) {
	// Token-aware guard: pay only small amounts.
	pay := workflow.MustNewTransition("pay", []workflow.Place{"payable"}, []workflow.Place{"paid"})
	gc, err := workflow.NewExpressionConstraint("token.amount <= 5000.0")
	if err != nil {
		t.Fatal(err)
	}
	pay.AddConstraint(gc)
	def, err := workflow.NewDefinition([]workflow.Place{"payable", "paid"}, []workflow.Transition{*pay})
	if err != nil {
		t.Fatal(err)
	}

	workflowtest.AssertGuardAllows(t, def, "pay", nil,
		workflow.NewToken(workflow.TokenData{"amount": 240.75}))
	workflowtest.AssertGuardRejects(t, def, "pay", nil,
		workflow.NewToken(workflow.TokenData{"amount": 9000.0}))
}

func TestClockAndDue(t *testing.T) {
	def := reviewNet(t)
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	clk := workflowtest.NewClock(t0)

	wf, err := workflow.NewWorkflow("wf", def, "draft", workflow.WithClock(clk.Now))
	if err != nil {
		t.Fatal(err)
	}
	workflowtest.Apply(t, wf, "submit")

	// Nothing due before the deadline; escalate due after it — no sleeping.
	workflowtest.AssertDue(t, wf, clk.Now())
	clk.Advance(72 * time.Hour)
	workflowtest.AssertDue(t, wf, clk.Now(), "escalate")

	clk.Set(t0)
	if !clk.Now().Equal(t0) {
		t.Fatalf("Set: clock = %v, want %v", clk.Now(), t0)
	}

	msg := expectTBFailure(t, func(tb testing.TB) {
		workflowtest.AssertDue(tb, wf, t0, "escalate")
	})
	if !strings.Contains(msg, "escalate") {
		t.Fatalf("AssertDue failure %q should name the expectation", msg)
	}
}
