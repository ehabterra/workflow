# Production recipes: crash windows, idempotency, and the seams between transactions

The library's core guarantee is narrow and strong: **one instance's state
change commits atomically** — marking, context, version, due index, and any
transactional side effects, in one transaction, under optimistic
concurrency. Everything *around* that guarantee (webhooks arriving twice,
processes dying between two saves, listeners re-running) is the host's job,
and every recipe here comes from building the reference system at
[`examples/expense_approval`](../../examples/expense_approval) — the file
and test that prove each one are named as we go.

Read [`MENTAL_MODEL.md`](./MENTAL_MODEL.md) first if the net vocabulary is
new, and [`docs/BOUNDARIES.md`](../BOUNDARIES.md) for what the library
deliberately does not do.

## The core loop: `Execute`, and what it re-runs

Every state change is `load → fire → save`. `Manager.Execute` is that cycle
with the failure handling built in: it loads fresh state, runs your `fn`,
and saves under the instance's version; if a concurrent writer saved first
(`ErrConflict`), it reloads and re-runs `fn` — up to `WithMaxRetries` times.

The subtlety that bites (dogfood friction #6): **anything your `fn` closure
captures must be reset at the top of `fn`**, because a conflict re-runs the
whole function on fresh state:

```go
var fired []string
err := mgr.Execute(ctx, id, def, func(wf *workflow.Workflow) error {
    fired = nil // ← WITHOUT this, a retry APPENDS to the previous attempt's names
    if err := wf.ApplyTransition("approve"); err != nil {
        return err
    }
    fired = append(fired, "approve")
    return nil
})
```

Forgetting the reset is silent: single-writer tests never conflict, so the
bug only appears under real concurrency, as duplicated history records or
double-counted metrics. The dogfood's `stepRecorder` resets its step list
at the top of every `fn` (`app.go: fire`), and `FireDue` does the same
internally with its fired list. Rule of thumb: the first lines of `fn`
re-initialize every variable `fn` writes.

The same reasoning gives the other two `Execute` rules:

- **`fn` must be re-runnable.** A transition no longer enabled on the
  reloaded state returns `ErrNotEnabled` out of `fn` — usually exactly what
  you want a redelivered webhook to see.
- **External side effects don't belong in `fn`.** `fn` may run more than
  once, and nothing un-sends an email. Put must-commit-with-state writes in
  a transactional side effect (next recipe); do fire-and-forget effects
  after `Execute` returns, keyed idempotently.

## Exactly-once audit trail (interactive and timer fires)

History is opt-in and the library never writes it for you — but it hands
you the transaction. `WithTxSideEffect` runs your write in the same
transaction as the save, so state and record commit or roll back together:

```go
var rec *history.TransitionRecord
err := mgr.Execute(ctx, id, def,
    func(wf *workflow.Workflow) error {
        rec = nil // retry reset, as above
        if err := wf.ApplyTransition("approve"); err != nil {
            return err
        }
        rec = &history.TransitionRecord{WorkflowID: id, Transition: "approve", Actor: actor, CreatedAt: now}
        return nil
    },
    workflow.WithTxSideEffect(func(ctx context.Context, tx any) error {
        return hist.SaveTransitionTx(ctx, tx.(*sql.Tx), rec)
    }))
```

Timer firings get the same guarantee through `WithFireDueTxSideEffect`,
which receives the steps the `FireDue` pass fired (transition + from/to
marking) *inside* the transaction — never write timer history after
`FireDue` returns, that reopens the crash window. See the worked example in
[`TIMERS_GUIDE.md`](./TIMERS_GUIDE.md).

The dogfood's `TestCrashConsistency` injects a failing side effect and
proves neither half lands; `TestFireDueTxSideEffect_EffectErrorRollsBack`
proves the timer variant re-fires and re-records exactly once.

## Webhooks: idempotency by error mapping

A webhook will be redelivered; design the handler so redelivery is boring.
The error split does most of the work (`app.go: Approve` and the HTTP
mapping in `server.go`):

| Result | Meaning | HTTP answer |
|---|---|---|
| `nil` | fired | 200/303, do the side effects |
| `ErrNotEnabled` | already fired earlier (or not yet reachable) | **200 no-op** — redelivery |
| `ErrGuardRejected` | state fine, policy says no | 403 |
| `ErrConflict` | lost a race | never seen — `Execute` retried it |

The first fire moves the token; the redelivered fire finds the input place
empty and gets `ErrNotEnabled`. Mapping that to success-no-op makes the
endpoint idempotent without a dedup table. (`TestWebhookRedelivery` pins
this: same request twice, one firing, one audit record.)

## Cross-instance steps: two transactions, a reconciler, and honest windows

The library is atomic *per instance*. A step that touches two instances —
the dogfood's expense → payment-net enqueue, and pay → `mark_paid` back on
the expense — is **two transactions**, and a crash between them is not a
bug you can eliminate; it is a window you must own. The recipe has three
parts:

**1. Order the writes so the window is detectable.** Commit the record of
truth first, then the follower. In the dogfood, `pay` moves the payment
token to `paid_out` (transaction 1) and only then advances the expense to
`paid` (transaction 2). A crash in between leaves a detectable
inconsistency: a `paid_out` token whose expense still says `approved` —
the token IS the outbox entry.

**2. Make both writes idempotent re-runs.** `EnqueueApproved` no-ops if the
expense's token is already in the payment net (`paymentHasExpense`);
`markPaid` is a transition that returns `ErrNotEnabled` when already fired.
Because both halves tolerate re-execution, *repairing* the window is just
*re-running* it.

**3. Run a periodic reconciler that closes every window you documented.**
The dogfood's `Reconcile` (`app.go`) is a loop over the fleet with one
`case` per crash window:

```go
switch {
case v.Has("approved") && paidOut[v.ID]:          // pay committed, mark_paid lost
    a.markPaid(ctx, v.ID)
case v.Has("approved") && !paymentHasExpense(pay, v.ID): // finalize committed, enqueue lost
    a.EnqueueApproved(ctx, v.ID)
case /* creation crash artifact */ :
    // see the creation-seed recipe below
}
```

Wired to a ticker (`-reconcile`, default 1m), it makes the system
self-healing: `TestReconcileRepairsCrashWindow` simulates each window and
proves one pass repairs it. If you prefer a classic outbox table over
state-derived detection, `WithTxSideEffect` is where you'd insert the
outbox row — same transaction as the state change, drained by the same
kind of periodic loop.

The general shape: **every multi-instance feature = ordered idempotent
writes + one reconciler case per seam.** Write the reconciler case in the
same PR as the feature, or the window ships undocumented.

## Creation seed: the first save's crash window

`CreateWorkflow` persists a bare instance; setting business context and
firing the first transition is a second save. The dogfood does both
mutations in ONE `Execute` (`SubmitExpense`: set context + route submit in
the same `fn`), so there is exactly one window — between create and that
first `Execute` — and its artifact is precisely characterizable: *a draft
with no context*.

That makes garbage collection safe and boring, as the third `Reconcile`
case:

```go
case v.Has("draft") && v.Amount == 0 && a.now().Sub(*v.DraftedAt) > draftGrace:
    a.mgr.DeleteWorkflow(ctx, v.ID) // creation crash artifact
```

Two details carry the safety: a successful creation can never look like
this (context and first fire commit together), and the grace period —
keyed off the draft token's `EnteredAt`, which the engine stamps — protects
a creation in flight right now. If your process must not even tolerate
that window, seed everything through `CreateWorkflowFromMarking` with the
initial state you want, then treat the instance as live only after your
first `Execute` succeeds.

## Listeners: at-least-once, always

Event listeners (`AddEventListener`) run when the transition fires *in
memory* — which, under `Execute`, can be more than once (retries), and can
also be once for a change that then fails to save. So listener side effects
are **at-least-once relative to persisted state**, in both directions.
Treat them as notifications, not records: counters, cache invalidation,
logs. The dogfood's metrics listener is the model — incrementing a gauge
twice is harmless. Anything that must agree with the database goes through
`WithTxSideEffect` instead.

## Token queries must not re-enter the workflow

`FindTokens`, `CountTokens`, `AggregateTokens`, and `TransformTokens` hold
the workflow's lock while they run your predicate. A predicate (or
transform) that calls back into the same workflow — `wf.GetTokens`,
`wf.ApplyTransition`, anything taking the lock — **deadlocks** (dogfood
friction #7). Collect first, act after:

```go
// WRONG: deadlocks — the predicate re-enters wf while the query holds its lock.
wf.AggregateTokens(func(t workflow.Token) bool {
    return len(wf.GetTokens("payable")) > 0 // re-entry
}, "amount")

// RIGHT: pre-collect, then run the query on plain data.
payable := wf.GetTokens("payable")
ids := make(map[workflow.TokenID]bool, len(payable))
for _, t := range payable {
    ids[t.ID()] = true
}
agg := wf.AggregateTokens(func(t workflow.Token) bool {
    return ids[t.ID()]
}, "amount")
```

Keep predicates pure functions of their arguments and this never comes up.

## Definitions evolve: make approval a policy

When a deployed definition changes, persisted instances still carry the old
fingerprint, and loads route through your `WithDefinitionMigration` hook.
The mismatch carries a structural `Diff` — places/transitions added,
removed, changed, by name — so the hook can be a policy instead of a rubber
stamp (dogfood `app.go`):

```go
workflow.WithDefinitionMigration(func(ctx context.Context, mm workflow.DefinitionMismatch) error {
    switch {
    case mm.Diff == nil: // state saved by an older library: no info — rely on place validation
        return nil
    case mm.Diff.Additive(): // new places/transitions only: cannot invalidate a marking
        return nil
    default:
        return fmt.Errorf("non-additive change (%s): needs explicit migration", mm.Diff)
    }
})
```

Non-additive changes then get *real* migration logic per case — the
dogfood's `migratePaymentAnchor` rewrites stored markings when a place was
deleted, idempotently and version-guarded, before the manager reloads.

## The checklist

For every feature that touches persistence, ask:

1. Does anything my `Execute` fn captures **reset at the top of fn**?
2. Does every must-agree-with-state write go through **`WithTxSideEffect`**
   (or `WithFireDueTxSideEffect` for timers)?
3. Is the external entry point idempotent under redelivery
   (**`ErrNotEnabled` → no-op**)?
4. If this step spans two instances: are the writes **ordered and
   idempotent**, and did I add the **reconciler case** for the window
   between them?
5. Are listener side effects safe to run **at-least-once**?
6. Do my token-query predicates avoid **re-entering the workflow**?
7. If the definition changed: is the migration hook's approval a
   **policy over the diff**, with explicit logic for non-additive cases?
