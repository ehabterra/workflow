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

> **Re-measured after #34 (dynamic-cardinality join) landed.** Both columns below
> come from the same reproducible rule (see *Re-running the measurement*), so the
> delta is apples-to-apples. The absolute numbers differ slightly from those
> reported in the #36 PR, which applied the same rule by hand and did not count
> each function's doc comment; the earlier figures are kept in the footnote.

| | after #36 | after #34 |
|---|---|---|
| **Declarative** (`workflow.yaml`) | 85 | **109** |
| **Go — orchestration** (branching, ledger, satisfaction, binding) | 208 | **157** |
| **Go — effect implementations** (the SQL itself) | 180 | **164** |
| **Total Go** | 388 | **321** |

### **Declarative coverage: 25% by line** (was 18%)

Counting orchestration only — the sequencing decisions, excluding the SQL that
would exist under any design — it is **41%**, up from 29%.

The headline this time is that **Go went down**, by 67 lines. #36 relocated
decisions from Go into the definition without deleting much; #34 deleted code
outright. Three functions and an entire effect are simply gone:

- `chainSatisfied` (26 lines) — the AND-join whose arity was `len(chain)`,
  including the `pending` parameter that forced the host to *simulate the write
  it was about to make*
- `approvedRoles` (17 lines) — the ledger read that fed it
- `roleInChain` (9 lines) — the authorization check the net could not express
- the `record_approval` effect and the `approvals` table it wrote (16 lines net)

What replaced them is four lines of YAML:

```yaml
require:
  - place: approvals
    where: "token.role in chain"
    distinct: role
    count: "len(chain)"
```

Line-counting YAML against Go flatters neither side — YAML is denser per line —
so here is the same question asked by **concern**, which is the fairer measure:

| # | Workflow concern | Where it lives |
|---|---|---|
| 1 | States / places | ✅ declarative |
| 2 | Legal transitions + resulting state | ✅ declarative |
| 3 | Initial marking | ✅ declarative |
| 4 | Terminal detection | ✅ declarative |
| 5 | Guard expressions | ✅ declarative |
| 6 | Guard *inputs* (`ready`, `sod_ok`, ~~`chain_satisfied`~~) | ⚠️ half since #34 — one of the three is gone; the rest need #35 |
| 7 | Ready-gate evaluation | ❌ Go (#35) |
| 8 | Approval chain resolution | ❌ Go — the ladder is domain policy, but the net now consumes it as a *value* rather than an answer |
| 9 | Approvals ledger | ✅ **declarative since #34** — the pool of colored tokens IS the ledger |
| 10 | Chain satisfaction (dynamic AND-join) | ✅ **declarative since #34** |
| 11 | Separation of duties | ⚠️ half — guard declarative, input Go |
| 12 | Last-resort override | ⚠️ half since #34 — a declared transition with its own guard and effects; the input is Go |
| 13 | Branch selection (partial vs final) | ✅ declarative since #36 |
| 14 | Per-transition effect binding | ✅ declarative since #36 |
| 15 | Effect ordering | ✅ declarative since #36 |
| 16 | Post-commit phase | ✅ declarative since #36 |
| 17 | Status projection | ⚠️ half since #36 — *when* it runs is declared, the place→status map is still host code (#39) |
| 18 | Multi-instance cascade | ❌ Go — **and incorrect**, see friction 5 |
| 19 | Guard rejection → error mapping | ❌ Go (#38) |
| 20 | Actor authorization (is this actor even in the chain?) | ⚠️ half since #34 — `role in chain` is the guard; the directory lookup is Go |

### **Declarative coverage: 68% by concern** (11 full + 5 half of 20) — was 50%

The target this document set after #36 was "**above 70%** by concern once #34,
#35 and #36 have all landed". Two of the three are in and it stands at 68%, so
the estimate is holding: #35 is what carries concerns 6, 7, 11 and 20, and it is
the only one of the three still open.

Read the shape rather than the number. What #34 moved is not plumbing — it is
the part that made this workflow hard. The net now holds the ledger, counts it,
de-duplicates it by role, and refuses an approver the chain does not require.
What is left below the line is the *inputs* to guards (#35), the multi-instance
cascade (#37), and error identity (#38).

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

**What it did not solve.** `chain` still arrives from the host via `SetContext`,
read before the transaction opens — so the *value* the join counts against can
still be stale. That is #35, and #34 does not touch it.

### 2 — Guards cannot query ([#35](https://github.com/ehabterra/workflow/issues/35)) · ~37 lines, plus a race — **now the top item**

Every guard in `workflow.yaml` still reads a boolean or a value the host
pre-computed: `ready`, `sod_ok`, `chain`. None can be computed by the guard,
because `envBuilder` receives an `Event` and an `Event` has no transaction.

#34 changed the *shape* of this problem without solving it. The satisfaction
test is no longer a pre-computed answer — the net works it out from tokens it
holds, under the same lock as the firing, so the ledger half of the race is
closed. What remains is the *inputs*: `chain` is derived from `req.Amount`, read
outside the transaction. If the requisition's value changes between the read and
the fire, the join counts against a chain that is no longer the right one.

That is a narrower race than before (it needs the record to change, not merely a
concurrent approval), but it is the same defect, and there is still nowhere to
put an in-transaction re-check.

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

### 6 — Guard rejections are not identifiable ([#38](https://github.com/ehabterra/workflow/issues/38)) · ~20 lines

`Submit` needs 422 for a failed ready-gate and `Approve` needs 403 for separation
of duties, but the library reports only that *a* guard rejected. The host recomputes
the failing condition to decide which error to return — `if !ready { return
ErrNotReady }` — duplicating the guard outside the net purely to produce the right
status code.

### 7 — Status projection is manual ([#39](https://github.com/ehabterra/workflow/issues/39)) · ~16 lines

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
- **effect implementations** — `registerEffects`, `resolveTarget`, `statusFor`,
  `asTx`, `str` in `effects.go`
- **excluded** — `App`/`New`/`Create`/`Status`/`Marking`/`Ledger` in `app.go`, and
  the role/directory/record/schema block in `domain.go`

Comparing across releases means measuring both revisions with this rule, not
comparing against a number quoted in an older PR. The figures reported when #36
landed (declarative 85, orchestration 167, effects 142) came from the same rule
applied by hand without counting doc comments; re-measured mechanically the same
tree gives 85 / 208 / 180.

**Target after [#34](https://github.com/ehabterra/workflow/issues/34),
[#35](https://github.com/ehabterra/workflow/issues/35), and
[#36](https://github.com/ehabterra/workflow/issues/36):** the same workflow above
**70%** by concern. Two of the three have landed and it stands at **68%**, so the
target is intact and #35 decides it. If it does not get there, that is the signal
to stop investing and keep the library scoped to nets that are genuinely parallel.
