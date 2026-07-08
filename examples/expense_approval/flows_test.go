package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/workflowtest"
)

// TestAutoApprovePettyCash: the guard-routed fast path. Amounts <= 100 skip
// review entirely (submit_auto), land in approved, queue for payment, and
// the batch pays them.
func TestAutoApprovePettyCash(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "alice", 50)

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("approved") {
		t.Fatalf("petty cash must auto-approve, marking %v", view.Places)
	}
	status, err := app.PaymentStatus(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("auto-approved expense must be queued for payment, got %q", status)
	}

	res, err := app.RunBatch(ctx, "op")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paid) != 1 || res.Paid[0] != id {
		t.Fatalf("batch must pay the petty-cash expense, got %+v", res)
	}

	recs, err := app.hist.ListHistory(ctx, id, history.QueryOptions{Transition: "submit_auto"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 {
		t.Fatalf("want a submit_auto audit record, got %d", len(recs))
	}
}

// TestSubmitRoutingBoundary pins the guard boundary: exactly 100 is petty
// cash, a cent more goes to review.
func TestSubmitRoutingBoundary(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()

	// The same boundary, table-tested straight against the definition — no
	// HTTP, no storage — with the workflowtest guard harness.
	workflowtest.AssertGuard(t, app.expenseDef, "submit_auto",
		workflowtest.GuardCase{Name: "petty cash", Context: map[string]any{"amount": 50.0}, Allow: true},
		workflowtest.GuardCase{Name: "at the limit", Context: map[string]any{"amount": 100.0}, Allow: true},
		workflowtest.GuardCase{Name: "a cent over", Context: map[string]any{"amount": 100.01}, Allow: false},
	)

	atLimit := mustSubmit(t, app, "alice", 100)
	view, err := app.Expense(ctx, atLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("approved") {
		t.Fatalf("100.00 must auto-approve, marking %v", view.Places)
	}

	over := mustSubmit(t, app, "alice", 100.01)
	view, err = app.Expense(ctx, over)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("pending_legal") || !view.Has("pending_finance") {
		t.Fatalf("100.01 must go to parallel review, marking %v", view.Places)
	}
}

// TestReviseAfterRejection drives the loop edge of the net: reject, let the
// stranded sibling escalate (cancellation-regions gap), then revise. The
// revision must clear the stranded round-one tokens by hand, quiet the due
// index, update the amount, and re-route through the submit guards — here to
// the petty-cash fast path.
func TestReviseAfterRejection(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "bob", 300)

	if _, err := app.Reject(ctx, id, "legal", "lawyer"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// The reject's reset arcs cancelled round one's finance token, so this
	// tick fires nothing (it used to fire a stranded escalation).
	later := time.Now().Add(73 * time.Hour)
	if fired, err := app.Tick(ctx, later); err != nil || len(fired) != 0 {
		t.Fatalf("tick after reject: fired %v, err %v (want nothing)", fired, err)
	}

	fired, err := app.Revise(ctx, id, "bob", "cheaper option", 90)
	if err != nil {
		t.Fatalf("revise: %v", err)
	}
	if !contains(fired, "revise") || !contains(fired, "submit_auto") {
		t.Fatalf("revise must re-route to the fast path, fired %v", fired)
	}

	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("approved") || view.Amount != 90 {
		t.Fatalf("want approved at 90 after revision, got %v amount %.2f", view.Places, view.Amount)
	}
	// The token surgery must leave no round-one residue.
	for _, p := range []string{"rejected", "pending_finance", "escalated_finance", "pending_legal", "escalated_legal"} {
		if view.Has(p) {
			t.Fatalf("stranded round-one token survived revision in %s: %v", p, view.Places)
		}
	}
	// And the due index must be quiet — no zombie escalation from round one.
	fired2, err := app.Tick(ctx, later.Add(200*time.Hour))
	if err != nil {
		t.Fatalf("tick after revise: %v", err)
	}
	if len(fired2) != 0 {
		t.Fatalf("no timers may fire after revision cleared the branches, got %v", fired2)
	}

	// Revising a non-rejected expense is refused.
	if _, err := app.Revise(ctx, id, "bob", "", 50); !errors.Is(err, workflow.ErrNotEnabled) {
		t.Fatalf("revise on a non-rejected expense: want ErrNotEnabled, got %v", err)
	}
}

// TestReviseBackToReview: a revision above the petty-cash limit goes through
// full parallel review again — the cycle can run more than once.
func TestReviseBackToReview(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "carol", 800)
	if _, err := app.Reject(ctx, id, "finance", "cfo"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Revise(ctx, id, "carol", "with receipts this time", 750); err != nil {
		t.Fatal(err)
	}
	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("pending_legal") || !view.Has("pending_finance") {
		t.Fatalf("revision above the limit must re-enter review, marking %v", view.Places)
	}
	// Round two is fully operable: reject again, revise again.
	if _, err := app.Reject(ctx, id, "legal", "lawyer"); err != nil {
		t.Fatalf("round-two reject: %v", err)
	}
	if _, err := app.Revise(ctx, id, "carol", "", 60); err != nil {
		t.Fatalf("round-two revise: %v", err)
	}
	view, _ = app.Expense(ctx, id)
	if !view.Has("approved") {
		t.Fatalf("round-three fast path must approve, marking %v", view.Places)
	}
}

// TestReleaseHeldPayment: the manual-review path for guard-held payouts.
func TestReleaseHeldPayment(t *testing.T) {
	app, _ := newTestApp(t)
	ctx := context.Background()
	id := mustSubmit(t, app, "dave", 9000)
	for _, branch := range []string{"legal", "finance"} {
		if _, err := app.Approve(ctx, id, branch, branch); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.EnqueueApproved(ctx, id); err != nil {
		t.Fatal(err)
	}

	// The batch holds it; status says so.
	res, err := app.RunBatch(ctx, "op")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paid) != 0 || res.Held != 1 {
		t.Fatalf("batch must hold the 9000 token, got %+v", res)
	}
	if status, _ := app.PaymentStatus(ctx, id); status != "held" {
		t.Fatalf("want status held, got %q", status)
	}

	// Manual release pays it out and advances the expense.
	if err := app.ReleasePayment(ctx, id, "cfo"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if status, _ := app.PaymentStatus(ctx, id); status != "paid" {
		t.Fatalf("want status paid after release, got %q", status)
	}
	view, err := app.Expense(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !view.Has("paid") {
		t.Fatalf("expense must be paid after release, marking %v", view.Places)
	}
	// The reviewer is on the record.
	recs, err := app.hist.ListHistory(ctx, paymentID, history.QueryOptions{Transition: "release"})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].Actor != "cfo" {
		t.Fatalf("release must be audited with the reviewer, got %+v", recs)
	}

	// Releasing again is an idempotent no-op; releasing something that was
	// never enqueued is a real error.
	if err := app.ReleasePayment(ctx, id, "cfo"); err != nil {
		t.Fatalf("redelivered release must no-op, got %v", err)
	}
	other := mustSubmit(t, app, "erin", 200)
	if err := app.ReleasePayment(ctx, other, "cfo"); !errors.Is(err, ErrNotPayable) {
		t.Fatalf("release of a non-enqueued expense: want ErrNotPayable, got %v", err)
	}
}

// TestReleaseAndReviseHTTP drives the new endpoints through the real HTTP
// surface, including flash redirects for browsers.
func TestReleaseAndReviseHTTP(t *testing.T) {
	ts, _ := newTestServer(t)
	id := submitViaHTTP(t, ts, "frank", "7500")
	for _, branch := range []string{"legal", "finance"} {
		postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {branch}})
	}
	postForm(t, ts.URL+"/batch/run", nil)

	// The batch page shows the held token with its release control.
	resp, err := http.Get(ts.URL + "/batch")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "held by guard") || !strings.Contains(string(page), "/payments/"+id+"/release") {
		t.Fatalf("batch page must offer release for the held token:\n%s", page)
	}

	code, body := postForm(t, ts.URL+"/payments/"+id+"/release", url.Values{"actor": {"cfo"}})
	if code != http.StatusOK || !strings.Contains(body, "Released payment for "+id) {
		t.Fatalf("release: %d %q", code, body)
	}
	code, _ = postForm(t, ts.URL+"/payments/exp-nope/release", nil)
	if code != http.StatusConflict {
		t.Fatalf("release of unknown expense: want 409, got %d", code)
	}

	// A browser gets a redirect carrying the flash message.
	req, _ := http.NewRequest("POST", ts.URL+"/expenses/"+id+"/reject", strings.NewReader("branch=legal"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "flash=") {
		t.Fatalf("browser action must redirect with flash, got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Revise over HTTP: paid expense can't be revised…
	code, _ = postForm(t, ts.URL+"/expenses/"+id+"/revise", url.Values{"amount": {"50"}})
	if code != http.StatusConflict {
		t.Fatalf("revise of a paid expense: want 409, got %d", code)
	}
	// …but a rejected one can.
	id2 := submitViaHTTP(t, ts, "grace", "400")
	postForm(t, ts.URL+"/expenses/"+id2+"/reject", url.Values{"branch": {"legal"}})
	code, body = postForm(t, ts.URL+"/expenses/"+id2+"/revise", url.Values{"amount": {"80"}, "actor": {"grace"}})
	if code != http.StatusOK || !strings.Contains(body, "auto-approved") {
		t.Fatalf("revise to petty cash: %d %q", code, body)
	}
}

// TestDashboardFilter: the stat chips filter the fleet by state.
func TestDashboardFilter(t *testing.T) {
	ts, _ := newTestServer(t)
	small := submitViaHTTP(t, ts, "alice", "40") // auto-approved
	large := submitViaHTTP(t, ts, "bob", "4000") // in review

	resp, err := http.Get(ts.URL + "/?state=approved")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), small) || strings.Contains(string(page), large) {
		t.Fatalf("filter must show only approved expenses (want %s, not %s):\n%s", small, large, page)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestDiagramsPages: the diagrams page and the live per-expense diagram
// render the engine's own Mermaid output, including the OR-input and
// reset-arc notations.
func TestDiagramsPages(t *testing.T) {
	ts, _ := newTestServer(t)
	id := submitViaHTTP(t, ts, "alice", "300")

	resp, err := http.Get(ts.URL + "/diagrams")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(page)
	for _, want := range []string{
		"flowchart TD",                 // the rich renderer (default top-down)
		"legal approve",                // humanized transition names
		`j_legal_approve{&#34;×&#34;}`, // OR-input ◇× exclusive gateway (quotes HTML-escaped)
		"class j_legal_approve gateway",
		"cancels",            // reset arcs
		"❰ amount ≤ 100.0 ❱", // visible guard
		"classDef timer",     // typed transitions
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("diagrams page missing %q:\n%s", want, body)
		}
	}

	resp, err = http.Get(ts.URL + "/expenses/" + id)
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "flowchart TD") || !strings.Contains(string(page), "class p_pending_legal current") {
		t.Fatal("detail page must embed the live diagram with the marking highlighted")
	}

	// The batch page carries the live token-flow diagram with badges.
	postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"legal"}})
	postForm(t, ts.URL+"/expenses/"+id+"/approve", url.Values{"branch": {"finance"}})
	resp, err = http.Get(ts.URL + "/batch")
	if err != nil {
		t.Fatal(err)
	}
	page, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "⬤×1") {
		t.Fatalf("batch page must show the live token badge:\n%s", page)
	}
}
