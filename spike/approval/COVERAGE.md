# Declarative-coverage measurement

> Spike for [#45](https://github.com/ehabterra/workflow/issues/45). Measures what
> fraction of a realistic approval workflow the library can express today, so the
> feature roadmap is driven by a number rather than by intuition.
>
> Run it: `go test ./spike/approval/...`

## What was built

A value-escalated purchase-requisition approval, implemented against **only what
the library ships today** — no proposed features, no workarounds the library
does not sanction. The domain is the shape found in real Go back-office
systems:

- a **threshold ladder** — the set of roles that must approve grows with the
  requisition's value, so the required approval count is not known until fire time
- an append-only **approvals ledger**
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

| | non-comment lines |
|---|---|
| **Declarative** (`workflow.yaml`) | **36** |
| **Go** (workflow logic the definition could not absorb) | **337** |
| *(excluded: domain schema, CRUD, role/directory config)* | *(206)* |

### **Declarative coverage: 10% by line**

Line-counting YAML against Go flatters neither side — YAML is denser per line — so
here is the same question asked by **concern**, which is the fairer measure:

| # | Workflow concern | Where it lives |
|---|---|---|
| 1 | States / places | ✅ declarative |
| 2 | Legal transitions + resulting state | ✅ declarative |
| 3 | Initial marking | ✅ declarative |
| 4 | Terminal detection | ✅ declarative |
| 5 | Guard expressions | ✅ declarative |
| 6 | Guard *inputs* (`ready`, `sod_ok`, `chain_satisfied`) | ❌ Go |
| 7 | Ready-gate evaluation | ❌ Go |
| 8 | Approval chain resolution | ❌ Go |
| 9 | Approvals ledger | ❌ Go |
| 10 | Chain satisfaction (dynamic AND-join) | ❌ Go |
| 11 | Separation of duties | ⚠️ half — guard declarative, input Go |
| 12 | Last-resort override | ❌ Go |
| 13 | Branch selection (partial vs final) | ⚠️ half — `ApplyAny` routes, effects don't follow |
| 14 | Per-transition effect binding | ❌ Go |
| 15 | Effect ordering | ❌ Go |
| 16 | Post-commit phase | ❌ Go |
| 17 | Status projection | ❌ Go |
| 18 | Multi-instance cascade | ❌ Go — **and incorrect**, see friction 5 |
| 19 | Guard rejection → error mapping | ❌ Go |
| 20 | Actor authorization (is this actor even in the chain?) | ❌ Go — added in review, see below |

### **Declarative coverage: 30% by concern** (5 full + 2 half of 20)

Both are far below the ~50% floor at which adoption starts paying for itself. The
issue-#45 estimate of 15-20% was about right for concerns and **optimistic** on
lines: the real line figure is 10%.

The shape of the result matters more than either number. What the library covers
is the **status graph** — real, correct, and the part a host can hand-roll in a
two-level map. What stays in Go is every concern that made this workflow hard.

## Friction log

Ranked by how much hand-written Go each one forces.

### 1 — Dynamic-cardinality join ([#34](https://github.com/ehabterra/workflow/issues/34)) · ~43 lines

`chainSatisfied` in `domain.go` is an AND-join whose arity is `len(chain)`, and
`chain` is not known until the requisition's value is read. The library joins over
statically declared input places, so the ledger, the required-set, and the
satisfaction test all live in Go.

The tell is `chainSatisfied`'s `pending` parameter: the caller must ask *"would the
chain be satisfied if this approval were recorded?"*, because the transition that
records it is the one whose guard needs the answer. The host simulates the write it
is about to make.

**The authorization half.** Because chain membership is a runtime value, *"is this
actor even a required approver?"* also cannot be a guard. The first draft of this
spike omitted the check and a reviewer caught it: any non-submitter — including an
actor holding no role at all, or one whose role is outside the required chain —
could write a permanent row into the append-only ledger and audit log. It could not
forge an approval (an out-of-chain role never satisfies the join, so no
unauthorized requisition can be approved), but the junk entry is permanent in an
append-only table.

`roleInChain` plus the check in `Approve` fixes it, and
`TestRolelessActorCannotApprove` / `TestOutOfChainActorCannotApprove` pin it. A
join over **role-tagged tokens** would subsume both halves: only a token carrying a
required role could enter the place, so membership would be structural rather than
a check the host has to remember to write.

### 2 — Guards cannot query ([#35](https://github.com/ehabterra/workflow/issues/35)) · ~40 lines, plus a race

Every guard in `workflow.yaml` reads a boolean the host pre-computed: `ready`,
`sod_ok`, `chain_satisfied`. None of them can be computed by the guard, because
`envBuilder` receives an `Event` and an `Event` has no transaction.

The consequence is not just volume. `Approve` does its entire phase-1 computation
**outside** the transaction, then fires. Between the read and the fire, another
approver can record an approval — and the guard will happily evaluate a
`chain_satisfied` that was true a moment ago. Optimistic concurrency catches a
concurrent *save* on the same instance, but not a stale *decision input* read
before the cycle began. Real systems close this with an in-transaction re-check;
here there is nowhere to put one.

### 3 — Effects are opaque closures ([#36](https://github.com/ehabterra/workflow/issues/36)) · ~150 lines

`WithTxSideEffect` takes one function. Everything else — which effects a transition
fires, in what order, and which of them differ per branch — is assembled by hand.
The `fire` wrapper, the `effect` type, the four effect constructors, the
per-transition effect lists, and the post-commit collection all exist for this
reason. It is the single largest block of Go in the spike.

The per-branch case is the sharpest: `Approve` fires `approve_partial` or
`approve_final`, and the branch is known only inside a Go closure, so the differing
effects are a `switch` rather than two transition declarations.

### 4 — No post-commit phase ([#36](https://github.com/ehabterra/workflow/issues/36)) · ~15 lines

The email must not be in the transaction. There is no phase for it, so the host
collects emails during the fire and flushes them after `Execute` returns —
re-implementing per action what a declared `after_commit` would do once.

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

Net effect: **11% → 10% by line, 32% → 30% by concern.**

## What worked well

Worth recording, because the spike is not an argument that the library is bad:

- **Atomic effects.** `TestEffectsAreAtomic` drops a table mid-transaction; the
  state change rolls back with it. The marking stays on `submitted` even though the
  transition fired in memory. This guarantee is real and it is the reason to build
  on this library rather than a bare FSM.
- **`ApplyAny` for branch routing** is the right primitive for "one action, two
  outcomes". It is only let down by effects not binding to the chosen transition.
- **The self-loop** (`approve_partial`: `submitted -> submitted`) models
  "record progress without advancing" cleanly and needed no special support.
- **Optimistic concurrency** is on by default and cost nothing to adopt.
- **Place metadata excluded from the fingerprint** is exactly right for anything
  that will later be authored in a UI.

## Re-running the measurement

The counts above are reproducible:

```bash
cd spike/approval
grep -vE '^\s*(#|$)' workflow.yaml | wc -l          # declarative lines
```

Go workflow-logic lines are the sum of the function spans listed in the table,
excluding `App`/`New`/`Create`/`Status`/`Marking` in `app.go` and the
role/directory/record/schema block in `domain.go`.

**Target after [#34](https://github.com/ehabterra/workflow/issues/34),
[#35](https://github.com/ehabterra/workflow/issues/35), and
[#36](https://github.com/ehabterra/workflow/issues/36):** the same workflow above
**70%** by concern. If three features do not get it there, that is the signal to
stop investing and keep the library scoped to nets that are genuinely parallel.
