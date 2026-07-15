# Thinking in Petri nets

This is the mental-model guide: how to *think* about a process so that
modeling it with this library becomes mechanical. Everything here is
grounded in the reference system at
[`examples/expense_approval`](../../examples/expense_approval) — each idea
names the file and concept that demonstrates it, so you can read the theory
and the running code side by side.

If you want API mechanics instead, start with the README and
[`WORKFLOW_BEST_PRACTICES.md`](./WORKFLOW_BEST_PRACTICES.md); for
data-carrying tokens see [`CPN_GUIDE.md`](./CPN_GUIDE.md); for deadlines see
[`TIMERS_GUIDE.md`](./TIMERS_GUIDE.md); for crash windows and idempotency
see [`PRODUCTION_RECIPES.md`](./PRODUCTION_RECIPES.md).

## A marking, not a status column

The habit to unlearn is `status VARCHAR`. A status column says a process is
in exactly one state at a time — and the moment your process has two things
in flight at once (legal review *and* finance review), one column can't say
so, and flags start accumulating around it.

A Petri net's state is a **marking**: the *set* of places currently holding
a token. "Place" is a state where work rests; "token" is the thing resting
there. An expense in parallel review has the marking
`{pending_legal, pending_finance}` — two places, one instance, no flags.
When you catch yourself adding a boolean next to a status column, you have
found a second place.

Everything else in the model is a consequence of this one move:

- A **transition** consumes tokens from its input places and produces tokens
  in its output places. That is the *only* way state changes.
- A transition is **enabled** when its input places are marked. Nothing
  fires on its own — some caller (a webhook handler, a batch job, a timer
  tick) applies it.
- A **guard** on a transition says whether the enabled transition may
  actually fire, based on data (`amount <= 100.0`).

## Parallelism is state, not threads

`submit` in the dogfood's `workflow.yaml` has two `to` places. Firing it
puts a token in `pending_legal` *and* `pending_finance` — an **AND-split**.
`finalize` has two `from` places — an **AND-join**, enabled only when both
branches have produced their token. There is no gateway object, no
goroutine, no join counter: parallelism is just arcs, and synchronization is
just a transition with two inputs.

This is why the model survives crashes so calmly. "Two things in flight"
is not two threads to keep alive — it is two tokens in a persisted marking.
Kill the process; the marking is still there; whoever loads it next sees
exactly what was in flight.

## Choice: who decides, and where

There are three kinds of "or" and they look different in the net:

- **XOR-split (choose one route out of a place).** Model it as *alternative
  transitions out of the same place*, each carrying a guard. The place is
  the decision point; the guards are the policy. The dogfood's `submit` vs
  `submit_auto` route by `amount`, resolved in one call:
  `wf.ApplyAny(ctx, "submit_auto", "submit")` fires the first candidate the
  state and guards allow.
- **OR-input (accept from either stage).** One transition with
  `from_any: true` is enabled by *any one* of its inputs and consumes only
  the marked one. The dogfood's `legal_approve` accepts from
  `pending_legal` OR `escalated_legal`, so escalation doesn't double the
  transition count. In diagrams this joins through a `◇×` gateway.
- **AND-join (wait for all).** Multiple `from` places, all required — the
  `◇+` gateway. Use it when branches must synchronize, not merely converge.

If you're unsure which you have, ask: *who decides?* Data decides → XOR
with guards. Whichever branch arrives first decides → OR-input. Nobody
decides, everyone must arrive → AND-join.

## Cycles are just arcs too

A rejected expense that can be revised and resubmitted is not a special
"loop" feature — it is a transition from `rejected` back to `draft`
(`revise` in the dogfood), after which the same submit guards route it
again, possibly down a different branch than round one. Nets are graphs;
nothing stops an arc from pointing "backwards".

## Cancellation is declared, not hand-rolled

When one branch's outcome makes the sibling branch moot (a rejection ends
the whole review), the reject transition declares **reset arcs**:
`resets: [pending_finance, escalated_finance, ...]`. Firing it clears those
places atomically with the move — pending tokens die, and any escalation
timer running on them dies too. Before this existed, the dogfood cleared
places by hand after firing and it was the friction log's top-ranked
finding; if you find yourself writing `ClearPlace` after a fire, you want a
reset arc.

## Time is modeled, never scheduled

A timed transition (`after: 72h`) does not start a timer. The library only
*records* when each token entered its place and can answer "what is due at
time T?" (`Due`, `NextDue`, `Manager.ListDue`). The host owns the clock: a
cron or ticker calls `ListDue` + `FireDue` with *its* `now`. Deadlines live
in the database, so they survive restarts by construction, and tests pass a
future `now` instead of sleeping. See
[`TIMERS_GUIDE.md`](./TIMERS_GUIDE.md).

## Two modeling styles: instance-per-entity vs shared pool

Most processes want **one workflow instance per business entity**: every
expense is its own instance, its marking is that expense's state, and the
"fleet" is just rows (`ListWorkflowIDs` + `LoadWorkflow`). Reach for this
by default — state stays local, contention stays per-entity, and one
stuck instance can't block the rest.

Sometimes entities genuinely *flow together*: a payment batch settles many
expenses at once. That is the **shared pool** style — one long-lived
instance whose places hold **colored tokens**, one per entity, carrying data
(`{expense_id, amount, submitter}`). The dogfood's `payment.yaml` is the
worked example: approved expenses become tokens in `payable`, the batch
fires `pay` per token, a guard holds big amounts for manual `release`. A
pool net legitimately starts **empty** (an empty marking is a valid
persisted state), and the cross-instance question "every payable token in
the system" is one indexed query (`Manager.ListPlaceTokens`).

The trade-offs between the two styles — and why the pool is a singleton
*by choice*, kept swappable by the token read-model — are worked through in
[`docs/roadmap/SHARED_POOL_MODELING.md`](../roadmap/SHARED_POOL_MODELING.md).
Token mechanics are in [`CPN_GUIDE.md`](./CPN_GUIDE.md).

## The engine never acts on its own

This is the boundary that makes everything above composable
(see [`docs/BOUNDARIES.md`](../BOUNDARIES.md) for the full list): the
library never fires a transition, never ticks a clock, never retries a side
effect. State is persisted; *code is not*. Every state change is some
caller doing `load → fire → save` — usually via `Manager.Execute`, which
wraps that cycle in optimistic concurrency and atomic side effects. What
looks like a limitation is the property that makes instances restart-safe,
replicas coordination-free, and tests deterministic.

## A worked translation

Take a request like: *"an order is picked and invoiced in parallel; both
must finish before shipping; invoicing over €10k needs a manager, who has
3 days before it escalates; a failed pick cancels the invoice work."*

- "picked and invoiced in parallel" → AND-split: `start` has `to:
  [picking, invoicing]`.
- "both must finish before shipping" → AND-join: `ship` has `from:
  [picked, invoiced]`.
- "over €10k needs a manager" → XOR-split out of `invoicing` with amount
  guards (`invoice_auto` vs `invoice_review`); the host fires
  `ApplyAny("invoice_auto", "invoice_review")`.
- "3 days before it escalates" → `after: 72h` on an `escalate` transition
  out of `pending_manager`; a host tick drives it.
- "manager approves whether or not it escalated" → OR-input:
  `manager_approve` with `from_any: true` from `[pending_manager,
  escalated]`.
- "a failed pick cancels the invoice work" → `pick_failed` carries
  `resets: [invoicing, pending_manager, escalated]`.

Six requirements, six constructs, zero custom plumbing. When a requirement
doesn't map onto a place, a transition, a guard, a reset, or a token — stop
and check [`docs/BOUNDARIES.md`](../BOUNDARIES.md): it is probably the
host's job (assignment, notification, cross-instance atomicity), and
[`PRODUCTION_RECIPES.md`](./PRODUCTION_RECIPES.md) has the pattern for it.
