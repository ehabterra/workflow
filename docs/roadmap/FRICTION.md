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

## Bugs found in the library (the dogfood earning its keep)

- **Lost update via read skew in `LoadVersionedState`** (both SQL backends,
  fixed during M5.1 hardening). The marking and the version were read in two
  separate queries; a commit in between paired a stale marking with the new
  version, and the next save from that snapshot passed the optimistic check
  and clobbered the concurrent write. Surfaced as a ~1%-flaky dogfood race
  test where a timer escalation resurrected an already-approved branch —
  invisible to every single-writer test. Now covered by the conformance
  kit's `Versioned/LoadIsAtomicSnapshot` (verified to fail on the old code).

- **`examples/migration_example` silently broken since M4**: its migration
  chain never added `due_at`, and example tests didn't run in CI. Caught the
  moment the CI examples job started running tests (also added during M5.1).

## Easy (credit where due)

- The M3.4 error split made webhook semantics one `errors.Is` switch:
  redelivery (`ErrNotEnabled`) vs forbidden (`ErrGuardRejected`) vs conflict.
- `Execute` + `WithTxSideEffect` gave a crash-consistent audit trail in ~20
  lines; the kill-test passed first try.
- `EnsureSchema` + `ListDue`/`FireDue`: the entire escalation cron is a
  ticker and two calls, and it survived `kill -9` + restart unchanged.
- `WithClock`/explicit-`now` design meant zero sleeps anywhere in the tests.
- The strict YAML loader caught every schema typo with a line number.

## What is missing — ranked (the M5 answer)

The dogfood app now exercises guards-from-context routing (XOR-split),
cycles (revise/resubmit), per-token manual release, AND-split/join,
timers, and the CPN batch — deliberately probing the library's limits.
Priority order for what the library needs next, from what actually hurt:

1. **Cancellation regions** (entries 1, and now the revise cycle). The
   workaround graduated from "host checks a flag" to **host-side token
   surgery**: `Revise` must `ClearPlace` six places by hand or round two
   double-fires reviews and inherits round one's stale escalation deadline.
   Every consumer with reject/timeout semantics on parallel branches will
   have to reinvent this. Highest priority by a wide margin.

2. **OR-input transitions** (entry 2). The escalation feature doubled the
   approve/reject transitions; with the revise loop the duplication now
   also infects every host-side action helper (`applyFirst` chains).

3. **XOR-split routing primitive** (new). Guard-routed alternatives out of
   one place (petty cash vs. review) work, but the host must try each
   variant and interpret guard rejections (`routeSubmit`). An engine-level
   "fire the enabled one of these" (free-choice conflict resolution) — or
   just a documented recipe — belongs in the library.

4. **`FireDue` side effects** (entry 4). Timer audit records are still
   at-least-once while every interactive fire is exactly-once. Grows worse
   as timers multiply.

5. **Additive-change detection for fingerprints** (new). Adding `release`,
   `revise`, and submit routing changed both nets' fingerprints; the
   migration hook can only see two opaque hashes, so the host approves
   blindly (safe here because the loader re-validates places — but the
   hook can't distinguish "new transition added" from "place renamed").
   A structural diff (places added/removed, transitions added/removed)
   handed to the migration hook would make upgrade hooks trustworthy.

6. **Creation seed** (entry 8) and **cross-instance recipes** (entry 5) —
   real but tolerable with documented patterns.

## Verdict so far

No bespoke persistence or scheduling layer was needed — the M5 exit
criterion holds. The library's core loop (load → fire → save atomically) is
solid; the friction is all at the edges (cancellation, OR-inputs, timer
audit atomicity, cross-instance recipes).
