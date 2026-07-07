// The M5 dogfood reference system (docs/DOGFOOD.md): a near-real
// expense-approval service built the way a host application would build it.
// One workflow instance per expense (parallel legal+finance review, 3-day
// escalation timers), plus one long-lived CPN payment net where approved
// expenses accumulate as colored tokens until a batch run pays them out.
package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	_ "embed"

	"github.com/ehabterra/workflow"
	"github.com/ehabterra/workflow/history"
	"github.com/ehabterra/workflow/storage"
	wfyaml "github.com/ehabterra/workflow/yaml"
)

//go:embed workflow.yaml
var expenseYAML []byte

//go:embed payment.yaml
var paymentYAML []byte

// paymentID is the single long-lived payment-net instance shared by the
// whole system.
const paymentID = "payment-batch"

// ErrTerminal is returned for actions on an expense that is already rejected.
// The net itself cannot cancel the sibling branch on rejection (no
// cancellation regions yet — see docs/DOGFOOD.md), so terminality is a host
// rule enforced here.
var ErrTerminal = errors.New("expense is already rejected")

// ErrNotPayable is returned by ReleasePayment for an expense with no token in
// the payment net's payable place (and none already paid out).
var ErrNotPayable = errors.New("nothing to release")

// payGuardLimit mirrors the payment net's pay guard (token.amount <= 5000.0):
// amounts above it are held for manual release. Kept in one place so the UI
// and PaymentStatus agree with the YAML guard.
const payGuardLimit = 5000.0

// schemaEnsurer is what both SQL storage backends provide beyond
// workflow.Storage; the app only needs it at startup.
type schemaEnsurer interface {
	workflow.Storage
	EnsureSchema(ctx context.Context) error
}

// App wires storage, history, and the two workflow definitions into the
// operations the HTTP layer (and the tests) call.
type App struct {
	db         *sql.DB
	hist       *history.SQLHistory
	mgr        *workflow.Manager
	expenseDef *workflow.Definition
	paymentDef *workflow.Definition
	now        func() time.Time
	metrics    *metrics
}

// NewApp builds the app on an open database. driver is "sqlite3" or "pgx";
// escalateAfter overrides the YAML's 72h escalation deadline when positive
// (safe for persisted instances: transition timeouts are not part of the
// definition fingerprint). nowFn is the host clock; tests inject a fake.
func NewApp(ctx context.Context, db *sql.DB, driver string, escalateAfter time.Duration, nowFn func() time.Time) (*App, error) {
	var store schemaEnsurer
	var hist *history.SQLHistory
	var err error
	switch driver {
	case "sqlite3":
		store, err = storage.NewSQLiteStorage(db)
		hist = history.NewSQLiteHistory(db)
	case "pgx":
		store, err = storage.NewPostgresStorage(db)
		hist = history.NewPostgresHistory(db)
	default:
		return nil, fmt.Errorf("unsupported driver %q (want sqlite3 or pgx)", driver)
	}
	if err != nil {
		return nil, fmt.Errorf("create storage: %w", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		return nil, fmt.Errorf("ensure state schema: %w", err)
	}
	if err := hist.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("ensure history schema: %w", err)
	}

	expenseDef, err := loadDefinition(expenseYAML)
	if err != nil {
		return nil, fmt.Errorf("load expense definition: %w", err)
	}
	paymentDef, err := loadDefinition(paymentYAML)
	if err != nil {
		return nil, fmt.Errorf("load payment definition: %w", err)
	}
	if escalateAfter > 0 {
		expenseDef.Transition("legal_escalate").SetTimeoutAfter(escalateAfter)
		expenseDef.Transition("finance_escalate").SetTimeoutAfter(escalateAfter)
	}

	// Fresh loads on every request: correct across replicas, and the demo is
	// nowhere near the scale where the registry cache would matter.
	//
	// The migration hook approves definition upgrades for both nets: every
	// change so far has been additive (new transitions — release, revise,
	// submit routing; no place was removed), so persisted markings stay
	// valid — and the loader still validates every loaded place after the
	// hook approves, so a marking that genuinely no longer fits fails
	// anyway. A host removing places would need real migration logic here.
	mgr := workflow.NewManager(workflow.NewRegistry(), store,
		workflow.WithDefinitionMigration(func(ctx context.Context, id, stored, current string) error {
			log.Printf("definition upgraded for %s (stored fingerprint %.8s… -> %.8s…): additive change, approving", id, stored, current)
			return nil
		}))

	a := &App{
		db:         db,
		hist:       hist,
		mgr:        mgr,
		expenseDef: expenseDef,
		paymentDef: paymentDef,
		now:        nowFn,
		metrics:    newMetrics(),
	}

	// Observer listener: count every firing, never block business flow.
	mgr.AddEventListener(workflow.EventAfterTransition, func(e workflow.Event) error {
		if t := e.Transition(); t != nil {
			a.metrics.inc(t.Name())
		}
		return nil
	})

	// The payment net exists exactly once; create it on first boot.
	if _, err := mgr.LoadWorkflow(ctx, paymentID, paymentDef); err != nil {
		if !errors.Is(err, workflow.ErrWorkflowNotFound) {
			return nil, fmt.Errorf("load payment net: %w", err)
		}
		if _, err := mgr.CreateWorkflow(ctx, paymentID, paymentDef, "batch_control"); err != nil {
			return nil, fmt.Errorf("create payment net: %w", err)
		}
	}
	return a, nil
}

func loadDefinition(raw []byte) (*workflow.Definition, error) {
	cfg, err := wfyaml.LoadConfigFromBytes(raw)
	if err != nil {
		return nil, err
	}
	return wfyaml.NewLoader().LoadDefinition(cfg)
}

// firedStep is one transition firing with the marking snapshotted around
// THAT firing — a compound action (approve + finalize, or a batch of pays)
// records each step's own from/to, not one aggregate pair for all of them.
type firedStep struct {
	name     string
	from, to string
	notes    string
}

// stepRecorder wraps a loaded instance so callers fire transitions through
// it and get per-step from/to snapshots for the audit trail.
type stepRecorder struct {
	wf    *workflow.Workflow
	steps []firedStep
}

// apply fires one transition, recording its own from/to marking.
func (r *stepRecorder) apply(ctx context.Context, name string) error {
	from := placesString(r.wf.CurrentPlaces())
	if err := r.wf.ApplyTransitionWithContext(ctx, name); err != nil {
		return err
	}
	r.steps = append(r.steps, firedStep{name: name, from: from, to: placesString(r.wf.CurrentPlaces())})
	return nil
}

// applyForToken fires a per-token transition (CPN); notes carries what moved
// (the place-set from/to of a token net often doesn't change, so the token
// identity is the informative part).
func (r *stepRecorder) applyForToken(ctx context.Context, name string, tok workflow.Token, notes string) error {
	from := placesString(r.wf.CurrentPlaces())
	if err := r.wf.ApplyTransitionForToken(ctx, name, tok.ID()); err != nil {
		return err
	}
	r.steps = append(r.steps, firedStep{name: name, from: from, to: placesString(r.wf.CurrentPlaces()), notes: notes})
	return nil
}

// note records a pseudo-step for a host-side mutation that is not a
// transition firing (e.g. dropping a token into the payment net).
func (r *stepRecorder) note(name, notes string) {
	m := placesString(r.wf.CurrentPlaces())
	r.steps = append(r.steps, firedStep{name: name, from: m, to: m, notes: notes})
}

// fire runs fn on the instance under Manager.Execute and commits one history
// record per fired step — each with the marking snapshotted around that
// step — in the same transaction as the state save (M3.5: state and audit
// trail can never disagree). It returns the fired transition names.
func (a *App) fire(ctx context.Context, id string, def *workflow.Definition, actor string, fn func(r *stepRecorder) error) ([]string, error) {
	var steps []firedStep
	err := a.mgr.Execute(ctx, id, def, func(wf *workflow.Workflow) error {
		rec := &stepRecorder{wf: wf} // Execute retries fn on ErrConflict; each attempt records fresh.
		if err := fn(rec); err != nil {
			steps = nil
			return err
		}
		steps = rec.steps
		return nil
	}, workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
		sqlTx, ok := tx.(*sql.Tx)
		if !ok {
			return fmt.Errorf("unexpected tx type %T", tx)
		}
		for _, s := range steps {
			rec := &history.TransitionRecord{
				WorkflowID: id,
				FromState:  s.from,
				ToState:    s.to,
				Transition: s.name,
				Notes:      s.notes,
				Actor:      actor,
				CreatedAt:  a.now(),
			}
			if err := a.hist.SaveTransitionTx(ctx, sqlTx, rec); err != nil {
				return err
			}
		}
		return nil
	}))
	if err != nil {
		return nil, err
	}
	names := make([]string, len(steps))
	for i, s := range steps {
		names[i] = s.name
	}
	return names, nil
}

// SubmitExpense creates a new expense instance and fires submit (the
// AND-split into the two review branches). Returns the new expense ID.
//
// Creation is two saves (CreateWorkflow, then the submit Execute); a crash
// between them leaves a context-less draft that Reconcile deletes. See
// docs/roadmap/FRICTION.md.
func (a *App) SubmitExpense(ctx context.Context, submitter, description string, amount float64) (string, error) {
	submitter = strings.TrimSpace(submitter)
	description = strings.TrimSpace(description)
	if submitter == "" {
		return "", errors.New("submitter is required")
	}
	if len(submitter) > 120 || len(description) > 500 {
		return "", errors.New("submitter or description is too long")
	}
	// Reject non-finite and absurd amounts: ParseFloat accepts "Inf" and
	// "NaN", and +Inf would poison the JSON context on save.
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 || amount > 1e12 {
		return "", errors.New("amount must be a positive number")
	}
	id, err := newExpenseID()
	if err != nil {
		return "", err
	}
	if _, err := a.mgr.CreateWorkflow(ctx, id, a.expenseDef, "draft"); err != nil {
		return "", fmt.Errorf("create expense: %w", err)
	}
	fired, err := a.fire(ctx, id, a.expenseDef, submitter, func(r *stepRecorder) error {
		r.wf.SetContext("submitter", submitter)
		r.wf.SetContext("description", description)
		r.wf.SetContext("amount", amount)
		return routeSubmit(ctx, r)
	})
	if err != nil {
		return "", err
	}
	// The petty-cash fast path lands directly in approved: queue it for the
	// payment batch (crash window repaired by Reconcile, as with finalize).
	if slices.Contains(fired, "submit_auto") {
		if err := a.EnqueueApproved(ctx, id); err != nil {
			log.Printf("enqueue auto-approved %s: %v (Reconcile will repair)", id, err)
		}
	}
	return id, nil
}

// routeSubmit fires whichever submit variant the amount guard enables — the
// XOR-split. There is no engine primitive for "fire the one enabled
// transition out of this place" (friction-logged), so the host tries each
// variant and treats a guard rejection as "not this route".
func routeSubmit(ctx context.Context, r *stepRecorder) error {
	var lastErr error
	for _, name := range []string{"submit_auto", "submit"} {
		err := r.apply(ctx, name)
		if err == nil {
			return nil
		}
		if !errors.Is(err, workflow.ErrTransitionNotAllowed) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("no submit route accepted the expense: %w", lastErr)
}

// Revise closes the loop on a rejection: the submitter updates the expense
// and resubmits. The net's revise transition moves rejected back to draft —
// but the sibling branch's stranded token (no cancellation regions) must be
// cleared BY HAND here, or round two would double-fire reviews and inherit a
// stale escalation deadline. This token surgery is exactly the friction the
// cancellation-regions milestone exists for (docs/roadmap/FRICTION.md).
func (a *App) Revise(ctx context.Context, id, actor, description string, amount float64) ([]string, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 || amount > 1e12 {
		return nil, errors.New("amount must be a positive number")
	}
	fired, err := a.fire(ctx, id, a.expenseDef, actor, func(r *stepRecorder) error {
		if !hasPlace(r.wf, "rejected") {
			return fmt.Errorf("%w: only a rejected expense can be revised", workflow.ErrNotEnabled)
		}
		if err := r.apply(ctx, "revise"); err != nil {
			return err
		}
		for _, p := range []workflow.Place{"pending_legal", "pending_finance", "escalated_legal", "escalated_finance", "legal_ok", "finance_ok"} {
			r.wf.ClearPlace(p) // cancel the stranded round-one review tokens
		}
		r.wf.SetContext("amount", amount)
		if description = strings.TrimSpace(description); description != "" {
			r.wf.SetContext("description", description)
		}
		return routeSubmit(ctx, r)
	})
	if err != nil {
		return nil, err
	}
	if slices.Contains(fired, "submit_auto") {
		if err := a.EnqueueApproved(ctx, id); err != nil {
			log.Printf("enqueue auto-approved %s: %v (Reconcile will repair)", id, err)
		}
	}
	return fired, nil
}

// Approve fires the branch's approve transition (from pending or escalated,
// whichever holds the token) and then tries finalize, so the AND-join fires
// as soon as the second branch approves.
//
// Webhook semantics ride on the M3.4 error split: a redelivered approval
// finds the branch already advanced and gets ErrNotEnabled (idempotent
// no-op); a rejected expense gets ErrTerminal (forbidden).
func (a *App) Approve(ctx context.Context, id, branch, actor string) ([]string, error) {
	if err := validBranch(branch); err != nil {
		return nil, err
	}
	return a.fire(ctx, id, a.expenseDef, actor, func(r *stepRecorder) error {
		if hasPlace(r.wf, "rejected") {
			return ErrTerminal
		}
		if err := applyFirst(ctx, r, branch+"_approve", branch+"_approve_escalated"); err != nil {
			return err
		}
		if err := r.apply(ctx, "finalize"); err != nil && !errors.Is(err, workflow.ErrTransitionNotAllowed) {
			return err
		}
		return nil
	})
}

// Reject fires the branch's reject transition. The sibling branch's token
// stays where it is (no cancellation regions); ErrTerminal on later actions
// is how the host closes the instance.
func (a *App) Reject(ctx context.Context, id, branch, actor string) ([]string, error) {
	if err := validBranch(branch); err != nil {
		return nil, err
	}
	return a.fire(ctx, id, a.expenseDef, actor, func(r *stepRecorder) error {
		if hasPlace(r.wf, "rejected") {
			return ErrTerminal
		}
		return applyFirst(ctx, r, branch+"_reject", branch+"_reject_escalated")
	})
}

// EnqueueApproved drops the expense into the payment net's payable place as
// a colored token, once (idempotent by expense_id). Called after an approval
// lands the expense in approved, and by Reconcile.
//
// This is a cross-instance step: the expense's state save and this token
// creation are two transactions. A crash between them is repaired by
// Reconcile (documented in docs/DOGFOOD.md — cross-instance atomicity is the
// host's job).
func (a *App) EnqueueApproved(ctx context.Context, expenseID string) error {
	view, err := a.Expense(ctx, expenseID)
	if err != nil {
		return err
	}
	if !view.Has("approved") && !view.Has("paid") {
		return fmt.Errorf("expense %s is not approved", expenseID)
	}
	_, err = a.fire(ctx, paymentID, a.paymentDef, "system", func(r *stepRecorder) error {
		if paymentHasExpense(r.wf, expenseID) {
			return nil // already enqueued (or already paid): no-op
		}
		if _, err := r.wf.CreateToken("payable", workflow.TokenData{
			"expense_id": expenseID,
			"amount":     view.Amount,
			"submitter":  view.Submitter,
		}); err != nil {
			return err
		}
		r.note("enqueue_payable", fmt.Sprintf("expense %s (%.2f)", expenseID, view.Amount))
		return nil
	})
	return err
}

// BatchResult reports one batch payment run.
type BatchResult struct {
	Paid    []string // expense IDs paid out
	Held    int      // tokens the pay guard held back (over the limit)
	Elapsed time.Duration
}

// RunBatch fires pay for every token in payable (the CPN component). The
// guard holds back amounts over the limit — those tokens stay in payable.
// Each paid expense's own instance is then advanced with mark_paid.
func (a *App) RunBatch(ctx context.Context, actor string) (*BatchResult, error) {
	start := a.now()
	res := &BatchResult{}
	_, err := a.fire(ctx, paymentID, a.paymentDef, actor, func(r *stepRecorder) error {
		res.Paid = nil
		res.Held = 0
		for _, tok := range r.wf.GetTokens("payable") {
			expID, _ := tokenExpenseID(tok)
			amount, _ := tok.Get("amount")
			err := r.applyForToken(ctx, "pay", tok, fmt.Sprintf("expense %s (%v)", expID, amount))
			if errors.Is(err, workflow.ErrGuardRejected) {
				res.Held++
				continue
			}
			if err != nil {
				return err
			}
			if expID != "" {
				res.Paid = append(res.Paid, expID)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Cross-instance follow-up: advance each paid expense. A crash here
	// leaves an expense approved-but-paid-out; Reconcile treats a paid_out
	// token as authoritative and re-fires mark_paid.
	for _, id := range res.Paid {
		if err := a.markPaid(ctx, id); err != nil {
			return res, fmt.Errorf("expense %s paid but mark_paid failed: %w", id, err)
		}
	}
	res.Elapsed = a.now().Sub(start)
	return res, nil
}

// ReleasePayment is the manual-review path for a guard-held token: a reviewer
// pays out one expense's token explicitly, bypassing the batch guard, with
// the reviewer recorded in the audit trail. Releasing an expense that is not
// in payable returns ErrNotPayable (already paid = redelivery no-op for the
// caller to map; never enqueued = genuine error).
func (a *App) ReleasePayment(ctx context.Context, expenseID, actor string) error {
	released := false
	_, err := a.fire(ctx, paymentID, a.paymentDef, actor, func(r *stepRecorder) error {
		released = false
		for _, tok := range r.wf.GetTokens("payable") {
			if id, ok := tokenExpenseID(tok); ok && id == expenseID {
				amount, _ := tok.Get("amount")
				if err := r.applyForToken(ctx, "release", tok, fmt.Sprintf("expense %s (%v) released by reviewer", expenseID, amount)); err != nil {
					return err
				}
				released = true
				return nil
			}
		}
		// Not in payable: distinguish "already paid out" from "never there".
		for _, tok := range r.wf.GetTokens("paid_out") {
			if id, ok := tokenExpenseID(tok); ok && id == expenseID {
				return nil // already paid: idempotent no-op
			}
		}
		return fmt.Errorf("%w: expense %s has no payable token", ErrNotPayable, expenseID)
	})
	if err != nil {
		return err
	}
	if released {
		return a.markPaid(ctx, expenseID)
	}
	return nil
}

// PaymentStatus reports where an expense stands in the payment net:
// "queued", "held" (guard will hold it; needs manual release), "paid", or ""
// (not enqueued).
func (a *App) PaymentStatus(ctx context.Context, expenseID string) (string, error) {
	wf, err := a.mgr.LoadWorkflow(ctx, paymentID, a.paymentDef)
	if err != nil {
		return "", err
	}
	for _, tok := range wf.GetTokens("payable") {
		if id, ok := tokenExpenseID(tok); ok && id == expenseID {
			if v, ok := tok.Get("amount"); ok {
				if amt, ok := v.(float64); ok && amt > payGuardLimit {
					return "held", nil
				}
			}
			return "queued", nil
		}
	}
	for _, tok := range wf.GetTokens("paid_out") {
		if id, ok := tokenExpenseID(tok); ok && id == expenseID {
			return "paid", nil
		}
	}
	return "", nil
}

func (a *App) markPaid(ctx context.Context, id string) error {
	_, err := a.fire(ctx, id, a.expenseDef, "system", func(r *stepRecorder) error {
		err := r.apply(ctx, "mark_paid")
		if errors.Is(err, workflow.ErrNotEnabled) {
			return nil // already marked (redelivery/reconcile): no-op
		}
		return err
	})
	return err
}

// Tick is the host-driven timer pass (M4): list overdue instances, fire
// their due transitions. Wired to a time.Ticker in main and to a fake clock
// in tests — same code path.
//
// History for timer firings is written after the state commit (FireDue owns
// its transaction and returns the fired names only afterwards), so unlike
// interactive fires it is at-least-once, not atomic — friction-logged.
func (a *App) Tick(ctx context.Context, now time.Time) (fired map[string][]string, err error) {
	ids, err := a.mgr.ListDue(ctx, now, 0)
	if err != nil {
		return nil, fmt.Errorf("list due: %w", err)
	}
	fired = make(map[string][]string)
	// One broken instance must not starve the rest of the fleet: keep
	// going and report every failure at the end.
	var errs []error
	for _, id := range ids {
		if id == paymentID {
			continue // the payment net has no timers
		}
		names, err := a.mgr.FireDue(ctx, id, a.expenseDef, now)
		if err != nil {
			errs = append(errs, fmt.Errorf("fire due %s: %w", id, err))
			continue
		}
		if len(names) == 0 {
			continue
		}
		fired[id] = names
		for _, name := range names {
			rec := &history.TransitionRecord{
				WorkflowID: id,
				Transition: name,
				Actor:      "timer",
				Notes:      "fired by escalation tick",
				CreatedAt:  now,
			}
			if err := a.hist.SaveTransition(ctx, rec); err != nil {
				errs = append(errs, fmt.Errorf("record timer history for %s: %w", id, err))
			}
		}
	}
	return fired, errors.Join(errs...)
}

// ReconcileReport says what a reconcile pass repaired.
type ReconcileReport struct {
	Enqueued      int // approved expenses added to the payment net
	Marked        int // expenses advanced to paid to match a paid_out token
	DraftsDeleted int // context-less drafts (creation crash artifacts) removed
}

// Reconcile repairs the documented crash windows: an approved expense
// missing from the payment net is enqueued; an expense with a paid_out
// token but no mark_paid is advanced; and a context-less draft older than
// draftGrace (a crash between CreateWorkflow and the submit Execute) is
// deleted. It keeps going past per-instance failures and reports them
// joined.
func (a *App) Reconcile(ctx context.Context) (*ReconcileReport, error) {
	views, err := a.ListExpenses(ctx)
	if err != nil {
		return nil, err
	}
	pay, err := a.mgr.LoadWorkflow(ctx, paymentID, a.paymentDef)
	if err != nil {
		return nil, err
	}
	paidOut := map[string]bool{}
	for _, tok := range pay.GetTokens("paid_out") {
		if id, ok := tokenExpenseID(tok); ok {
			paidOut[id] = true
		}
	}
	rep := &ReconcileReport{}
	var errs []error
	for _, v := range views {
		switch {
		case v.Has("approved") && paidOut[v.ID]:
			if err := a.markPaid(ctx, v.ID); err != nil {
				errs = append(errs, fmt.Errorf("mark %s paid: %w", v.ID, err))
				continue
			}
			rep.Marked++
		case v.Has("approved") && !paymentHasExpense(pay, v.ID):
			if err := a.EnqueueApproved(ctx, v.ID); err != nil {
				errs = append(errs, fmt.Errorf("enqueue %s: %w", v.ID, err))
				continue
			}
			rep.Enqueued++
		case v.Has("draft") && v.Amount == 0 && v.DraftedAt != nil && a.now().Sub(*v.DraftedAt) > draftGrace:
			// A successful SubmitExpense fires submit in the same save that
			// sets the context, so a lingering context-less draft can only
			// be a creation crash artifact. The grace period protects a
			// creation that is in flight right now.
			if err := a.mgr.DeleteWorkflow(ctx, v.ID); err != nil {
				errs = append(errs, fmt.Errorf("delete stale draft %s: %w", v.ID, err))
				continue
			}
			rep.DraftsDeleted++
		}
	}
	return rep, errors.Join(errs...)
}

// draftGrace is how old a context-less draft must be before Reconcile
// treats it as a crash artifact rather than a creation in flight.
const draftGrace = 5 * time.Minute

// --- read side (dashboard / detail pages) ---

// ExpenseView is what the UI renders for one expense.
type ExpenseView struct {
	ID          string
	Submitter   string
	Description string
	Amount      float64
	Places      []string
	State       string
	NextDue     *time.Time
	DraftedAt   *time.Time // when the draft token entered (set only while in draft)
	History     []history.TransitionRecord
}

// Has reports whether the expense's marking includes the place.
func (v *ExpenseView) Has(place string) bool {
	for _, p := range v.Places {
		if p == place {
			return true
		}
	}
	return false
}

// Branch reports where the branch's token sits: "pending", "escalated",
// "approved", or "" once the branch token has moved on (joined or the
// expense was rejected). The detail page draws the lanes from this.
func (v *ExpenseView) Branch(branch string) string {
	switch {
	case v.Has(branch + "_ok"):
		return "approved"
	case v.Has("escalated_" + branch):
		return "escalated"
	case v.Has("pending_" + branch):
		return "pending"
	default:
		return ""
	}
}

// Open reports whether review actions still apply.
func (v *ExpenseView) Open() bool {
	return !v.Has("rejected") && !v.Has("approved") && !v.Has("paid")
}

// Branches lists the parallel review branches, in display order.
func (v *ExpenseView) Branches() []string { return []string{"legal", "finance"} }

// Expense loads one expense for display (no history; see ExpenseWithHistory).
func (a *App) Expense(ctx context.Context, id string) (*ExpenseView, error) {
	wf, err := a.mgr.LoadWorkflow(ctx, id, a.expenseDef)
	if err != nil {
		return nil, err
	}
	return a.viewOf(id, wf), nil
}

// ExpenseWithHistory loads one expense plus its audit trail.
func (a *App) ExpenseWithHistory(ctx context.Context, id string) (*ExpenseView, error) {
	view, err := a.Expense(ctx, id)
	if err != nil {
		return nil, err
	}
	recs, err := a.hist.ListHistory(ctx, id, history.QueryOptions{Limit: 100})
	if err != nil {
		return nil, err
	}
	view.History = recs
	return view, nil
}

// ListExpenses loads the fleet for the dashboard (paged under the hood).
func (a *App) ListExpenses(ctx context.Context) ([]*ExpenseView, error) {
	var views []*ExpenseView
	const pageSize = 100
	for offset := 0; ; offset += pageSize {
		ids, err := a.mgr.ListWorkflowIDs(ctx, workflow.ListOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if id == paymentID {
				continue
			}
			wf, err := a.mgr.LoadWorkflow(ctx, id, a.expenseDef)
			if err != nil {
				return nil, fmt.Errorf("load %s: %w", id, err)
			}
			views = append(views, a.viewOf(id, wf))
		}
		if len(ids) < pageSize {
			break
		}
	}
	sort.Slice(views, func(i, j int) bool { return views[i].ID < views[j].ID })
	return views, nil
}

// PaymentView is what the UI renders for the payment net.
type PaymentView struct {
	Payable      []workflow.Token
	PaidOut      []workflow.Token
	PayableTotal float64
	PaidOutTotal float64
}

// Payment loads the payment net for the batch page, using token aggregation
// for the totals.
func (a *App) Payment(ctx context.Context) (*PaymentView, error) {
	wf, err := a.mgr.LoadWorkflow(ctx, paymentID, a.paymentDef)
	if err != nil {
		return nil, err
	}
	payableTokens := wf.GetTokens("payable")
	paidTokens := wf.GetTokens("paid_out")
	// Scope the aggregation predicates by pre-collected IDs — a predicate
	// must not call back into the workflow while it iterates.
	payable := wf.AggregateTokens(memberOf(payableTokens), "amount")
	paid := wf.AggregateTokens(memberOf(paidTokens), "amount")
	return &PaymentView{
		Payable:      payableTokens,
		PaidOut:      paidTokens,
		PayableTotal: payable.Sum,
		PaidOutTotal: paid.Sum,
	}, nil
}

func (a *App) viewOf(id string, wf *workflow.Workflow) *ExpenseView {
	v := &ExpenseView{ID: id}
	for _, p := range wf.CurrentPlaces() {
		v.Places = append(v.Places, string(p))
	}
	sort.Strings(v.Places)
	if s, ok := wf.Context("submitter"); ok {
		v.Submitter, _ = s.(string)
	}
	if d, ok := wf.Context("description"); ok {
		v.Description, _ = d.(string)
	}
	if amt, ok := wf.Context("amount"); ok {
		switch n := amt.(type) {
		case float64:
			v.Amount = n
		case int:
			v.Amount = float64(n)
		}
	}
	if due, ok := wf.NextDue(); ok {
		v.NextDue = &due
	}
	if v.Has("draft") {
		for _, tok := range wf.GetTokens("draft") {
			if at, ok := tok.EnteredAt(); ok {
				v.DraftedAt = &at
				break
			}
		}
	}
	v.State = stateLabel(v)
	return v
}

// stateLabel collapses the marking into the one-word status the dashboard
// shows.
func stateLabel(v *ExpenseView) string {
	switch {
	case v.Has("paid"):
		return "paid"
	case v.Has("approved"):
		return "approved"
	case v.Has("rejected"):
		return "rejected"
	case v.Has("draft"):
		return "draft"
	case v.Has("escalated_legal") || v.Has("escalated_finance"):
		return "escalated"
	default:
		return "in review"
	}
}

// --- helpers ---

// applyFirst tries the transitions in order, recording the one that fires.
// ErrNotEnabled means "token isn't there" — try the next source place; any
// other error (including ErrGuardRejected) stops immediately.
func applyFirst(ctx context.Context, r *stepRecorder, names ...string) error {
	var lastErr error
	for _, name := range names {
		err := r.apply(ctx, name)
		if err == nil {
			return nil
		}
		if !errors.Is(err, workflow.ErrNotEnabled) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

func hasPlace(wf *workflow.Workflow, place workflow.Place) bool {
	for _, p := range wf.CurrentPlaces() {
		if p == place {
			return true
		}
	}
	return false
}

func tokenExpenseID(t workflow.Token) (string, bool) {
	v, ok := t.Get("expense_id")
	if !ok {
		return "", false
	}
	id, ok := v.(string)
	return id, ok
}

func paymentHasExpense(wf *workflow.Workflow, expenseID string) bool {
	found := wf.FindTokens(func(t workflow.Token) bool {
		id, ok := tokenExpenseID(t)
		return ok && id == expenseID
	})
	return len(found) > 0
}

// memberOf returns a predicate matching exactly the given tokens (by ID).
func memberOf(tokens []workflow.Token) workflow.TokenPredicate {
	ids := make(map[workflow.TokenID]bool, len(tokens))
	for _, t := range tokens {
		ids[t.ID()] = true
	}
	return func(t workflow.Token) bool { return ids[t.ID()] }
}

// ErrBadBranch marks client input naming a review branch that doesn't exist;
// the HTTP layer maps it to 400.
var ErrBadBranch = errors.New("unknown branch (want legal or finance)")

func validBranch(branch string) error {
	if branch != "legal" && branch != "finance" {
		return fmt.Errorf("%w: %q", ErrBadBranch, branch)
	}
	return nil
}

func placesString(places []workflow.Place) string {
	ss := make([]string, len(places))
	for i, p := range places {
		ss[i] = string(p)
	}
	sort.Strings(ss)
	return strings.Join(ss, ",")
}

func newExpenseID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "exp-" + hex.EncodeToString(b[:]), nil
}

// --- metrics (listener-based; contrib/otel extraction is M5.3) ---

type metrics struct {
	mu      sync.Mutex
	firings map[string]int64
}

func newMetrics() *metrics {
	return &metrics{firings: make(map[string]int64)}
}

func (m *metrics) inc(transition string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.firings[transition]++
}

// snapshot returns the counters sorted by transition name for stable output.
func (m *metrics) snapshot() []struct {
	Name  string
	Count int64
} {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]struct {
		Name  string
		Count int64
	}, 0, len(m.firings))
	for name, n := range m.firings {
		out = append(out, struct {
			Name  string
			Count int64
		}{name, n})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
