# M5 friction log

Every papercut found while building the dogfood reference system
(`examples/expense_approval`, spec in `docs/DOGFOOD.md`), recorded as it was
hit. This list is the input for re-prioritizing the parked milestones —
graduate entries to GitHub issues at the end of M5.1.

Format: what was attempted → what the library made easy / hard / impossible →
workaround.

## Hard / missing

1. **No cancellation regions** (parked milestone, now dogfood-confirmed).
   Rejecting one review branch cannot cancel the sibling branch's token; the
   "rejected is terminal" rule had to move into the host (an `ErrTerminal`
   check before every decision). Workaround is fine but every consumer with
   an OR-outcome AND-split will rewrite it. Follow-on observed while
   hardening: the stranded branch's **72h timer still fires** on the closed
   expense — one spurious escalation, then the timer goes quiet (escalated
   has no further timeout). Harmless here and covered by
   `TestStrandedBranchEscalatesHarmlessly`, but with a *recurring* or
   chained timer a rejected instance would stay hot in the due index
   forever.

2. **"From pending OR escalated" doubles the transition count.** A
   transition's inputs are fixed places, so approve/reject each need a
   `*_escalated` twin (13 transitions where ~8 feel necessary). Workaround:
   `applyFirst(...)` helper trying each variant and treating `ErrNotEnabled`
   as "try the next source place". An OR-input transition (or place
   wildcard/region) would collapse this.

3. **A marking with zero places cannot persist** (`loaded state has no
   places` on load). A pure token-pool net whose real places can all be
   empty needs an always-marked `batch_control` place as an anchor. Cheap,
   but undiscoverable until it bites at reload time — should at least be
   documented, or empty markings allowed for token nets.

4. **`FireDue` cannot join a caller transaction.** Interactive fires get
   atomic state+history via `Execute` + `WithTxSideEffect`, but `FireDue`
   returns the fired names only after its own save commits, so timer audit
   records are written post-hoc (at-least-once; a crash between commit and
   history write loses the record). Wanted: a `FireDue` option receiving the
   fired transitions *inside* the transaction.

5. **Cross-instance steps are the host's problem** (expected — but the
   recipe deserves docs). Expense → payment-net enqueue and pay →
   `mark_paid` are two transactions each; both crash windows needed an
   explicit `Reconcile`. An outbox/reconcile recipe belongs in the M6 guides.

6. **`Execute` retry resets are subtle.** Any state captured by the `fn`
   closure (fired-transition names, before/after markings) must be reset at
   the top of `fn` because `Execute` re-runs it on `ErrConflict`. Easy to
   get silently wrong — a `workflowtest` assertion or a doc callout would
   help (M5.2 candidate).

7. **Token-query predicates must not call back into the workflow** (lock
   re-entrancy). An `AggregateTokens` predicate that calls `wf.GetTokens`
   risks deadlock; had to pre-collect IDs. Needs a doc warning on the
   `token_query.go` API.

8. **Instance creation can't seed context or fire in one save.**
   `CreateWorkflow(ctx, id, def, place)` persists a bare instance; setting
   the business context and firing `submit` needs a second save (`Execute`).
   A crash in between leaves a context-less draft the app must garbage-
   collect (`Reconcile` deletes them after a grace period, keyed off the
   draft token's `EnteredAt`). Wanted: `CreateWorkflow` variants taking
   initial context, or an `Execute` that can create-if-missing.

## Easy (credit where due)

- The M3.4 error split made webhook semantics one `errors.Is` switch:
  redelivery (`ErrNotEnabled`) vs forbidden (`ErrGuardRejected`) vs conflict.
- `Execute` + `WithTxSideEffect` gave a crash-consistent audit trail in ~20
  lines; the kill-test passed first try.
- `EnsureSchema` + `ListDue`/`FireDue`: the entire escalation cron is a
  ticker and two calls, and it survived `kill -9` + restart unchanged.
- `WithClock`/explicit-`now` design meant zero sleeps anywhere in the tests.
- The strict YAML loader caught every schema typo with a line number.

## Verdict so far

No bespoke persistence or scheduling layer was needed — the M5 exit
criterion holds. The library's core loop (load → fire → save atomically) is
solid; the friction is all at the edges (cancellation, OR-inputs, timer
audit atomicity, cross-instance recipes).
