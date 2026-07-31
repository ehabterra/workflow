# Declarative-coverage measurement

> Spike for [#45](https://github.com/ehabterra/workflow/issues/45). Measures what
> fraction of a realistic approval workflow the library can express today, so the
> feature roadmap is driven by a number rather than by intuition.
>
> Run it: `go test ./internal/spike/approval/...`

## What was built

A value-escalated purchase-requisition approval, implemented against **only what
the library ships today** — no proposed features, no workarounds the library
does not sanction. The domain is the shape found in real Go back-office
systems:

- a **threshold ladder** — the set of roles that must approve grows with the
  requisition's value, so the required approval count is not known until fire time
- an append-only **approvals ledger** — since #34 this is a pool of colored
  tokens in the net itself, not a host table
- **separation of duties** — the submitter may not approve their own record
- an **admin last-resort** for chains containing a role nobody holds
- **revision supersession** — approving a requisition supersedes prior approved ones
- **audit / notification / outbox** effects that must commit with the state change,
  plus a **post-commit** email

All of it works and is covered by tests, including the two-step chain, the
last-resort path, atomic effect rollback, and the state divergence documented below.

## The number

Counting only **workflow logic** — what states exist, which transitions are legal,
what guards them, what effects they fire, and what state results. Schema, record
CRUD, the role ladder config, and the user directory are domain code that would
exist with or without the library, and are excluded from both sides.

> **Re-measured after #35 (transaction-scoped guards) landed** — the last of the
> three features #46 said would decide whether this library is worth adopting.
> All columns come from the same reproducible rule (see *Re-running the
> measurement*), with one bucket renamed; that rename is called out below because
> it changes what the middle row means.

| | after #36 | after #34 | after #35 |
|---|---|---|---|
| **Declarative** (`workflow.yaml`) | 85 | 109 | **111** |
| **Go — orchestration** (sequencing, branching, satisfaction) | 208 | 157 | **159** |
| **Go — host implementations** (the SQL itself: effects, and now guard queries) | 180 | 164 | **222** |
| **Total Go** | 388 | 321 | **381** |

### **Declarative coverage: 23% by line** — *down* from 25%

Report the uncomfortable number first. **Total Go went UP by 60 lines**, and it
is not an accounting trick: the bucket rename moved `txGuardEnv` (58 lines) into
host implementations, and that function is genuinely new code.

What it replaced was smaller. `readyGate(req)` was a 12-line pure function over
an already-loaded struct, and `sod_ok` was one expression. What replaced them is
three SQL queries with their own error handling, registered once at startup. The
same thing happened at #36 — a registry entry is more verbose than the closure
it replaces — and it is the honest cost of making something addressable by a
definition instead of inlined in a call path.

The line count is measuring the wrong thing here, which is exactly why this
document has always carried a second measure.

### **Declarative coverage: 80% by concern** (14 full + 4 half of 20) — was 68%

| # | Workflow concern | Where it lives |
|---|---|---|
| 1 | States / places | ✅ declarative |
| 2 | Legal transitions + resulting state | ✅ declarative |
| 3 | Initial marking | ✅ declarative |
| 4 | Terminal detection | ✅ declarative |
| 5 | Guard expressions | ✅ declarative |
| 6 | Guard *inputs* (~~`ready`~~, ~~`sod_ok`~~, ~~`chain_satisfied`~~) | ✅ **declarative since #35** — all three pre-computed booleans are gone |
| 7 | Ready-gate evaluation | ✅ **declarative since #35** — `tx_guard: "readyGate()"` |
| 8 | Approval chain resolution | ⚠️ half — the ladder is host policy by design, but since #35 the net verifies its INPUT in the transaction (`amountOf() == amount`) |
| 9 | Approvals ledger | ✅ declarative since #34 — the pool of colored tokens IS the ledger |
| 10 | Chain satisfaction (dynamic AND-join) | ✅ declarative since #34 |
| 11 | Separation of duties | ✅ **declarative since #35** — `tx_guard: "actor != submitterOf()"`, read live |
| 12 | Last-resort override | ⚠️ half — a declared transition; the eligibility input is org data |
| 13 | Branch selection (partial vs final) | ✅ declarative since #36 |
| 14 | Per-transition effect binding | ✅ declarative since #36 |
| 15 | Effect ordering | ✅ declarative since #36 |
| 16 | Post-commit phase | ✅ declarative since #36 |
| 17 | Status projection | ⚠️ half since #36 — *when* it runs is declared, the place→status map is still host code (#39) |
| 18 | Multi-instance cascade | ❌ Go — **and incorrect**, see friction 5 |
| 19 | Guard rejection → error mapping | ❌ Go (#38) |
| 20 | Actor authorization | ⚠️ half — `role in chain` is the guard; the directory lookup is org data |

**The target is met.** This document set "**above 70%** by concern once #34, #35
and #36 have all landed" before any of them existed. All three are in, and it is
**80%**.

Read what is left, because the shape of the residue is the real result. Of the
six concerns not fully declarative, **four are deliberately out of scope**: the
role ladder, the directory, and last-resort eligibility are org modelling, which
`docs/BOUNDARIES.md` says stays in the host and which this measurement agrees
should. The two that are genuine gaps are the multi-instance cascade (#37) and
error identity (#38) — both small, both filed, neither load-bearing for the
question #46 asked.

### An unplanned result: the authorization hole closed itself

The first draft of this spike let any non-submitter write a permanent row into
the append-only ledger; a reviewer caught it and the fix was a hand-written check
the host had to remember (`roleInChain`, plus a branch in `Approve`).

That class of bug is now **unreachable in this shape**, and not because of the
check. The approval is a token created *inside* the `Execute` cycle, so appending
it and firing the transition are one atomic unit: if the net refuses the firing,
`Execute` returns before saving and the token never existed.
`TestRolelessActorCannotApprove` and `TestOutOfChainActorCannotApprove` now
assert an empty pool rather than an empty table, and they pass without the host
check being load-bearing — it survives only to choose between 403 and 409, which
is #38's job.

### What #34 cost

Recorded so the entry is not one-sided:

- **The pool is a place, and places are visible.** `approvals` appears in the net
  and in every diagram, and it has to be excluded from status projection by
  carrying no `status` metadata. A modeller has to understand that a place can
  be a pool rather than a state.
- **A third approve transition.** The last-resort path became its own transition
  (`approve_last_resort`) rather than a flag inside a satisfaction function. That
  is more declaration for less Go, but it is more declaration — the +24
  declarative lines are mostly it.
- **`require` is the third way a transition can be conditional**, after guards
  and `from_any` — and `require` cannot be combined with `from_any`, because both
  resolve which input a firing consumes. That is a rule an author has to learn,
  and the engine rejects the combination at `NewDefinition` rather than at run
  time.

## Friction log

Ranked by how much hand-written Go each one forces.

### 1 — Dynamic-cardinality join ([#34](https://github.com/ehabterra/workflow/issues/34)) — ✅ **RESOLVED**

*Was: ~43 lines, and the single reason the approval brain stayed hand-written.*

`chainSatisfied` was an AND-join whose arity was `len(chain)`, and `chain` was
not known until the requisition's value was read. The library joined over
statically declared input places, so the ledger, the required-set and the
satisfaction test all lived in Go.

All three are now in the definition. `approvals` is a place holding one colored
token per approval; `approve_final` declares:

```yaml
require:
  - place: approvals
    where: "token.role in chain"     # only chain members count
    distinct: role                   # two approvals from one role are one
    count: "len(chain)"              # resolved at fire time
```

The tell is gone with it. `chainSatisfied` took a `pending` parameter because the
caller had to ask *"would the chain be satisfied IF this approval were
recorded?"* — the transition that records the approval was the one whose guard
needed the answer. Now the approval is a token, the host appends it, and the net
answers a question about the state that actually exists.

**The authorization half** is subsumed as predicted: `where` means an
out-of-chain approval can never count toward the join, and `approve_partial`'s
`role in chain` guard refuses to record one at all. See *An unplanned result*
above for why this is now structural rather than a check.

**What #34 did not solve, and #35 did.** `chain` still arrives from the host via
`SetContext`, read before the transaction opens, so the value the join counts
against could be stale. #35 does not move the ladder into the definition — it is
org policy and stays out by design — but the approve branches now carry
`amountOf() == amount`, which verifies inside the transaction that the input the
chain was derived from has not moved. See friction 2.

**One thing still not expressible.** A `require:` expression is evaluated against
the workflow context and the place's tokens, NOT against the tx-guard
environment — so `count:` cannot itself call a query. Here that is covered by the
guard checking the chain's input instead, but a join whose arity is a pure
function of host data would still need the host to resolve it. Worth a follow-up
issue rather than a workaround.

### 2 — Guards cannot query ([#35](https://github.com/ehabterra/workflow/issues/35)) — ✅ **RESOLVED**

*Was: ~37 lines, plus a race with nowhere to close it.*

Every guard used to read a boolean the host had pre-computed — `ready`,
`sod_ok` — because `envBuilder` receives an `Event` and an `Event` has no
transaction. Worse than the volume was the timing: `Approve` did its whole
phase-1 computation OUTSIDE the transaction and then fired, so a value that
changed in between was silently stale. Optimistic concurrency catches a
concurrent *save*; it does not catch a stale *decision input*.

`tx_guard:` closes it. The expression is evaluated inside the firing
transaction, against an environment the host builds FROM that transaction:

```yaml
- name: submit
  tx_guard: "readyGate()"                                   # queries the lines

- name: approve_final
  tx_guard: "actor != submitterOf() && amountOf() == amount" # reads the record
```

`readyGate` and `sod_ok` are gone from Go entirely. The `amountOf() == amount`
half is the interesting one: the approval chain is derived from the
requisition's value, which is host policy and stays in Go — but the guard now
verifies, inside the transaction, that the value the chain was derived from has
not moved. `TestStaleChainIsRefusedInTheTransaction` raises a requisition from
1,000 to 20,000 between the read and the fire; the firing that would have
approved a 20,000 requisition on one signature is refused, and leaves no trace.

**What it cost.** A transaction is now held open across the caller's `fn`, its
guards, and every listener they trigger — documented as a boundary rather than
hidden. And the query environment is more code than the booleans it replaced
(see the headline figures); this is the second time a feature has traded volume
for addressability, and it is worth saying plainly both times.

**A design defect it surfaced.** The first implementation had the builder
*replace* the guard environment. Every real tx guard turned out to compare
something read live against something the host passed in (`actor !=
submitterOf()`), so replacing meant `actor` silently evaluated to nil and the
guard quietly answered wrong. The builder's entries are merged into the standard
environment instead. Found by converting this spike, not by review — which is
the argument for keeping the spike compiling against every feature.

### 3 — Effects are opaque closures ([#36](https://github.com/ehabterra/workflow/issues/36)) — ✅ **RESOLVED**

*Was: ~150 lines, the single largest block of Go in the spike.*

`WithTxSideEffect` took one function, so which effects a transition fired, in what
order, and which differed per branch was all assembled by hand: a `fire` wrapper
threading effect-builders through `Execute`, an `effect` type, four effect
constructors, a per-transition effect list in every service method, and a `switch`
on which branch `ApplyAny` picked.

All of that is now declared. `workflow.yaml` names the effects per transition;
`effects.go` registers the implementations once at startup; `App.fire` is eight
lines that seed context and apply. The per-branch switch is gone — `approve_partial`
and `approve_final` carry their own effect lists, so the branch that wins fires its
own effects with no host involvement.

What remains is the implementations themselves, which are host SQL and always were.
They grew because a registry entry must extract its params from the event and
assert the tx type where a closure captured typed values. (The 34 → 142 figure
reported at the time predates the mechanical counting rule; see *Re-running the
measurement*.)

### 4 — No post-commit phase ([#36](https://github.com/ehabterra/workflow/issues/36)) — ✅ **RESOLVED**

*Was: ~15 lines.*

The email must not be in the transaction. `after_commit:` is now a declared phase;
the host registers one `email` implementation and the definition says which
transitions send which template. The per-action collect-and-flush is gone.

The at-least-once boundary is documented on `AfterCommitFunc` rather than left for
the adopter to discover.

### 5 — No atomic multi-instance transition ([#37](https://github.com/ehabterra/workflow/issues/37)) · ~10 lines, **and a correctness bug**

This one does not merely cost lines — the honest implementation is **wrong**.

Superseding prior approved requisitions needs their instances to transition too,
inside the same transaction. The library has no way to do that, so
`supersedePriorEffect` issues a raw SQL `UPDATE`. The prior requisitions' status
becomes `Superseded` while **their workflow markings stay on `approved`**. The two
disagree, permanently, and nothing in the library notices.

`TestSupersedeCascade_DivergesMarking` asserts the divergence rather than guarding
against it:

```text
DIVERGENCE (expected, documents issue #37): status=Superseded marking=[approved]
```

That test should be **inverted into a correctness test** once atomic multi-instance
transitions land. Any real adopter hits this the moment they have revisions, and
the workaround silently creates state the engine does not know about.

### 6 — `require:` cannot use the transaction environment ([#34](https://github.com/ehabterra/workflow/issues/34) + [#35](https://github.com/ehabterra/workflow/issues/35)) · new

The two features that pair everywhere else do not compose here. A requirement's
`count:` / `where:` expressions see the workflow context and the place's tokens;
they do not see the `tx_guard` environment, so a join whose arity is a function
of host data still needs the host to resolve that data and inject it.

The spike works around it honestly — the guard verifies the chain's input in the
transaction (friction 2) — and the workaround is sound, because the chain itself
is org policy that belongs in the host anyway. But a net whose join size is a
pure database fact would have no such excuse. Filed as a follow-up rather than
papered over.

### 7 — Guard rejections are not identifiable ([#38](https://github.com/ehabterra/workflow/issues/38)) · ~20 lines

`Submit` needs 422 for a failed ready-gate and `Approve` needs 403 for separation
of duties, but the library reports only that *a* guard rejected. The host recomputes
the failing condition to decide which error to return — `if !ready { return
ErrNotReady }` — duplicating the guard outside the net purely to produce the right
status code.

### 8 — Status projection is manual ([#39](https://github.com/ehabterra/workflow/issues/39)) · ~16 lines

Place metadata carries a `status` label, `statusOf` derives it, and `project`
writes it in the transaction — repeated for every action. Forget it on one
transition and the record silently reports a stale status. Note the design that
already works: place metadata is **not** part of `Fingerprint()`, so labels and
coordinates can change without invalidating running instances.

## What peer review changed — and why it counts as data

Both numbers moved **down** after review, which is the right direction for a
measurement that exists to be honest rather than flattering.

**A dead transition, undetected.** The definition declared a `supersede`
transition that **nothing ever fired**. The cascade cannot fire it (friction 5), so
it is done in raw SQL, and the transition sat in the net as unreachable surface —
while being counted toward the declarative line total. Neither the library nor the
test suite noticed; a reviewer did.

That is unplanned evidence for [#42](https://github.com/ehabterra/workflow/issues/42).
Dead-transition detection is the *first* check on that issue's list, this is a
hand-authored net written by someone who knows the library, and the defect still
shipped and inflated the headline number of a PR whose entire purpose was an
honest measurement. A net authored in a UI by a non-developer will not do better.
The transition is removed; declarative lines went 39 → 36.

**A missing authorization check.** See friction 1 above — concern 20 in the table,
and ~11 more lines of Go.

Net effect on the pre-#36 baseline: **11% → 10% by line, 32% → 30% by concern.**

### Round two, on #34: separation of duties had a hole

Review found that an **admin could approve a requisition they had submitted
themselves**. Two things said yes independently: the host computed `sod_ok` as
`actor != submitter || lastResort`, exempting the escape hatch, and
`approve_last_resort` carried no `sod_ok` guard at all, because "last resort"
felt like its own justification. Being able to break a chain nobody can satisfy
is not a licence to sign off on your own record.

Both halves are fixed. `sod_ok` is now plainly `actor != req.Submitter`, and all
three approve branches carry it — so the rule is uniform and stated in the
definition rather than assembled from a boolean the host computes.
`TestAdminSubmitterCannotSelfApprove` pins it, including that a *different* admin
can still use the hatch.

Worth recording for the same reason as the first round: this **predates #34** —
the `|| lastResort` term is in the original spike — and neither the tests nor the
earlier review caught it. That is the second real defect found by review in a net
small enough to read in one sitting, which argues for
[#42](https://github.com/ehabterra/workflow/issues/42) better than any argument
about it does. Orchestration went 151 → 157 lines, because the fix is worth
explaining in place.

### Round three, on #35: two fail-open guards

Review found both, and both are the same class of mistake — a check whose
*failure direction* had never been chosen.

**A separation-of-duties check that failed open.** The environment exposed
`submitterOf()` and the guard read `actor != submitterOf()`. On any query error —
including a missing row — it returned `""`, and no actor equals `""`, so the
check PASSED. An unreadable requisition was approvable.

The proposed fix was a sentinel value nothing could equal. That does not work,
for exactly the reason the bug exists: `actor != sentinel` is *also* true. The
comparison had to move inside the function. `sodOk()` owns the whole question and
returns false on any read failure, which is the only shape that lets a failure
fall the safe way. `TestSodOkFailsClosed` pins all three directions.

**The last-resort branch skipped the amount check.** The other two approve
branches carry `amountOf() == amount`; this one had only the SoD half. But
`last_resort` is itself derived from the chain, which is derived from the amount
— so the branch most in need of the check was the one without it, and the file's
own header comment claimed otherwise. All three now carry identical text, which
is the point: a rule you can only verify by reading three different expressions
is a rule that will drift again.

That is the third consecutive round in which review found a correctness hole in
this net, and the second in which the hole was in the escape hatch. The pattern
is worth naming: `approve_last_resort` exists to bypass a rule, and every time it
has been written it has quietly bypassed a rule nobody meant it to.

## What worked well

Worth recording, because the spike is not an argument that the library is bad:

- **Atomic effects.** `TestEffectsAreAtomic` drops a table mid-transaction; the
  state change rolls back with it. The marking stays on `submitted` even though the
  transition fired in memory. This guarantee is real and it is the reason to build
  on this library rather than a bare FSM.
- **A token created inside `Execute` commits with the firing.** This is what makes
  the ledger safe: append the approval, fire, and either both land or neither
  does. It needed no new feature — it falls out of "a fire mutates memory only;
  persistence is a separate, version-guarded step".
- **`require` + `ApplyAny` compose exactly as hoped.** Three approve branches in
  declaration order, each enabled by its own condition, and the host asks for the
  first that fires. No branch table, no satisfaction test, no switch.
- **`distinct`** turned out to matter more than the count. Without it the join is
  satisfiable by one enthusiastic approver, and that is the kind of rule a host
  forgets to write. `TestPartialApprovalIsNotEnoughEvenTwice` pins it.
- **`ApplyAny` for branch routing** is the right primitive for "one action, several
  outcomes".
- **The self-loop** (`approve_partial`: `submitted -> submitted`) models
  "record progress without advancing" cleanly and needed no special support.
- **Optimistic concurrency** is on by default and cost nothing to adopt.
- **Place metadata excluded from the fingerprint** is exactly right for anything
  that will later be authored in a UI.

## Re-running the measurement

Declarative lines:

```bash
cd internal/spike/approval
grep -vE '^\s*(#|$)' workflow.yaml | wc -l
```

Go workflow-logic lines are the sum of **function spans including each
function's doc comment**, from `func` line to the closing brace at column 0:

- **orchestration** — `fire`, `Submit`, `Approve`, `Reject`, `Resubmit`,
  `Revoke`, `classify` in `app.go`, plus `readyGate` in `domain.go`
- **host implementations** — `registerEffects`, `resolveTarget`, `statusFor`,
  `asTx`, `str` in `effects.go`, plus `txGuardEnv` in `domain.go`. Renamed from
  "effect implementations" at #35: `txGuardEnv` is the SQL behind the guards,
  the exact counterpart of the SQL behind the effects, and putting it in
  orchestration would have called a query a sequencing decision. The rename is
  why the middle row moves at #35 — apply it to both revisions when comparing.
- **excluded** — `App`/`New`/`Create`/`Status`/`Marking`/`Ledger` in `app.go`, and
  the role/directory/record/schema block in `domain.go`

Comparing across releases means measuring both revisions with this rule, not
comparing against a number quoted in an older PR. The figures reported when #36
landed (declarative 85, orchestration 167, effects 142) came from the same rule
applied by hand without counting doc comments; re-measured mechanically the same
tree gives 85 / 208 / 180.

**The target set before any of them existed:** the same workflow above **70%** by
concern once [#34](https://github.com/ehabterra/workflow/issues/34),
[#35](https://github.com/ehabterra/workflow/issues/35) and
[#36](https://github.com/ehabterra/workflow/issues/36) had all landed — with the
explicit clause that failing it was the signal to stop investing and keep the
library scoped to nets that are genuinely parallel.

All three have landed. It is **80%**, so the investment is answered: build the
adoption floor (#40, #41) and the authoring track (#42 → #43, #44), and invert
`TestSupersedeCascade_DivergesMarking` when #37 lands.

Keep re-running this. The value of the spike is not the number it produced once
— it is that it compiles against every feature and re-measures, and it has now
caught a design defect (#35's environment) and two correctness holes (the #34
authorization gap, the last-resort SoD bypass) that neither the test suite nor
review found first.
