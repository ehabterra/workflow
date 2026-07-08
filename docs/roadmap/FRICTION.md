# M5 friction log

Every papercut found while building the dogfood reference system
(`examples/expense_approval`, spec in `docs/DOGFOOD.md`), recorded as it was
hit. This list is the input for re-prioritizing the parked milestones —
graduate entries to GitHub issues at the end of M5.1.

Format: what was attempted → what the library made easy / hard / impossible →
workaround.

## Hard / missing

1. ✅ **SHIPPED: cancellation regions via reset arcs** (was: no cancellation
   regions). A transition now declares places it clears atomically when it
   fires (`resets:` in YAML, `SetResets` in Go); the dogfood's reject
   transitions cancel the sibling branch — token, timer and all — and
   `Revise` dropped its six-place `ClearPlace` surgery. Original finding
   kept below for the record.

   **Original entry** (parked milestone, now dogfood-confirmed).
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

2. ✅ **SHIPPED: OR-input transitions (`from_any`)** — one transition now
   accepts from any one of its input places, consuming only it; the dogfood
   collapsed its four `*_escalated` twins and deleted the `applyFirst`
   helper. Original finding kept below.

   **Original entry**: "From pending OR escalated" doubles the transition count. A
   transition's inputs are fixed places, so approve/reject each need a
   `*_escalated` twin (13 transitions where ~8 feel necessary). Workaround:
   `applyFirst(...)` helper trying each variant and treating `ErrNotEnabled`
   as "try the next source place". An OR-input transition (or place
   wildcard/region) would collapse this.

3. ✅ **SHIPPED: empty markings persist** (was: a marking with zero places
   cannot persist). A marking whose places are all empty now saves, loads,
   and round-trips: `NewWorkflowFromMarking` and the new
   `Manager.CreateWorkflowFromMarking` accept an empty start, YAML may omit
   `initial_marking`, and the conformance kit's `EmptyMarkingRoundTrip`
   covers every backend. The dogfood deleted its `batch_control` anchor —
   and, since dropping a place is the one definition change
   approve-and-revalidate can't wave through, its migration hook grew real
   migration logic (`migratePaymentAnchor` strips the anchor from stored
   markings; `TestPaymentAnchorMigration`). Original finding kept below.

   **Original entry**: a pure token-pool net whose real places can all be
   empty needs an always-marked `batch_control` place as an anchor
   (`loaded state has no places` on load). Cheap, but undiscoverable until
   it bites at reload time — should at least be documented, or empty
   markings allowed for token nets.

   → Graduated into a wider design discussion: the anchor is a symptom of
   modeling a shared cross-instance pool as a single hot marking. Options
   (singleton pool / per-instance + read-model / sharded batches / queue /
   native shared place), pros-cons, research (fusion vs substitution,
   rcwf static places, proclets, WF-net soundness, Camunda/Temporal/saga),
   and open questions are in [`SHARED_POOL_MODELING.md`](./SHARED_POOL_MODELING.md).
   The empty-marking fix above was step 1 of that doc's chosen direction
   ("B via storage-only"); step 2 — normalizing the marking into a token
   child table with a cross-instance read-model
   (`workflow.TokenQueryStorage` / `Manager.ListPlaceTokens`) — has shipped
   too, closing the doc's open work (see its §9.6 for the as-built shape).

4. ✅ **SHIPPED: `WithFireDueTxSideEffect`** (was: `FireDue` cannot join a
   caller transaction). A `FireDue`-scoped tx side effect now receives the
   pass's fired steps — transition plus the marking on each side — *inside*
   the save's transaction, so a timer firing and its audit record commit or
   roll back together, exactly like an interactive fire. The dogfood's
   `Tick` deleted its post-hoc `SaveTransition` loop, its timer records
   gained from/to markings, and plain `Execute` rejects the option (only
   `FireDue` knows what fired). Original finding kept below.

   **Original entry**: interactive fires get atomic state+history via
   `Execute` + `WithTxSideEffect`, but `FireDue` returns the fired names
   only after its own save commits, so timer audit records are written
   post-hoc (at-least-once; a crash between commit and history write loses
   the record). Wanted: a `FireDue` option receiving the fired transitions
   *inside* the transaction.

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

1. ✅ **SHIPPED: cancellation regions** — implemented as reset arcs
   immediately after this ranking was written; the dogfood adopted them and
   deleted its token surgery (see resolved entry 1 above).

2. ✅ **SHIPPED: OR-input transitions** — `from_any` (see resolved entry 2
   above).

3. ✅ **SHIPPED: XOR-split routing** — `Workflow.ApplyAny(ctx, names...)`
   fires the first allowed candidate, skipping not-enabled and
   guard-rejected ones; the dogfood's `routeSubmit` is now one call.

4. ✅ **SHIPPED: `FireDue` side effects** — `WithFireDueTxSideEffect` (see
   resolved entry 4 above); timer audit records are exactly-once now.

5. ✅ **SHIPPED: structural diffs for the migration hook** (was:
   additive-change detection for fingerprints). Every save now stamps a
   compact definition *shape* (place names + per-transition record hashes)
   alongside the fingerprint, so a mismatch hands the hook a
   `DefinitionMismatch` with a **`DefinitionDiff`**: places and transitions
   added/removed/changed, by name — a rename reads as remove+add with the
   referencing transitions marked changed. `Diff.Additive()` makes "new
   structure only, approve mechanically" a one-line policy; the dogfood's
   hook now refuses non-additive expense-net changes instead of approving
   blindly, and state saved by pre-shape versions yields a nil diff ("no
   information"). Original finding: the hook could only see two opaque
   hashes, so the host approved blindly and couldn't distinguish "new
   transition added" from "place renamed".

6. **Creation seed** (entry 8) and **cross-instance recipes** (entry 5) —
   real but tolerable with documented patterns.

## Verdict so far

No bespoke persistence or scheduling layer was needed — the M5 exit
criterion holds. The library's core loop (load → fire → save atomically) is
solid; the friction is all at the edges (cancellation, OR-inputs, timer
audit atomicity, cross-instance recipes).
