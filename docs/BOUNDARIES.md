# Boundaries — what this library deliberately does not do

Honest statements about where the library's responsibility ends and the
host application's begins. None of these are roadmap gaps; they are design
decisions, and most of the library's reliability properties *follow from*
them. Each one names the pattern for living on the host side of the line —
worked versions are in
[`guides/PRODUCTION_RECIPES.md`](./guides/PRODUCTION_RECIPES.md) and running
in [`examples/expense_approval`](../examples/expense_approval).

## 1. Not a durable-execution engine

**State is persisted; code is not.** There is no replayed event history, no
resumable function, no captured call stack (the Temporal/Restate model). A
crash loses all in-memory progress since the last save — by design.
Recovery is: load the marking, look at it, decide what to drive next.

What this buys: nothing to version-pin about your code's execution history,
no non-determinism rules for what code may do, and a state model you can
inspect with a SQL query. What it costs: multi-step *host* logic between
saves needs its own crash story — which is recipe #1's `Execute` (make the
whole step one atomic save) and the reconciler pattern for steps that span
saves.

## 2. No internal clock or scheduler

The library never wakes up. Timed transitions record when tokens entered
their places and answer "what is due at time T?" (`Due`, `Manager.ListDue`);
firing what is due is a host loop calling `FireDue` with the host's `now`
(see [`guides/TIMERS_GUIDE.md`](./guides/TIMERS_GUIDE.md)). Deadlines live
in the database, so they survive restarts by construction; tests pass a
future `now` instead of sleeping; and running two schedulers against the
same fleet is safe because `FireDue` runs under the same optimistic
concurrency as every other save.

## 3. Listeners run at-least-once relative to persisted state

Event listeners fire when a transition executes *in memory*. Under
`Execute` retries the same logical change can run listeners more than once,
and a change that fails to save still ran them once. So listener effects
must be idempotent or harmless to repeat (metrics, cache invalidation) —
they are notifications, not records. Writes that must agree with the
database go through the transactional hook (`WithTxSideEffect`,
`WithFireDueTxSideEffect`), where the library *does* guarantee atomicity
with the state change. The library provides the transaction hook, not the
idempotency.

## 4. History is opt-in

No transition is recorded automatically. The audit trail is a separate
store you write to — either through the transactional hook (exactly-once,
the recommended path) or the `yaml.ApplyTransitionByNameWithHistory` helper.
Silent automatic history sounds convenient until you need to control the
record's fields, actor, or transaction — so the library never grew it.

## 5. Organizational structure stays in the host

Users, roles, assignment, delegation, escalation *targets* — none of it is
modeled here (see
[`guides/ORGANIZATIONAL_FEATURES_EXPLAINED.md`](./guides/ORGANIZATIONAL_FEATURES_EXPLAINED.md)).
The net models *what state the work is in*; *who may act* is a guard over
context you supply (`hasRole(...)` and friends), and *who is asked* is your
notification layer's job. Every org chart is different; a workflow engine
that ships one is wrong for everyone but its author.

**Where this boundary is NOT.** "Wait until a runtime-resolved set of
participants has acted" is net semantics, not org modelling, and the library
does express it: a dynamic-cardinality join (`require:`) counts tokens in a
place against an expression evaluated at fire time, so an approval chain whose
length comes from the record needs no host-side satisfaction check. What stays
out is still everything about *who those participants are* — the chain itself
is a value you resolve and hand in, and the roles a token carries mean nothing
to the engine beyond being data it can count and de-duplicate.

## 5a. A transaction-scoped guard holds a transaction open across your code

`tx_guard:` exists because a decision made before a transaction opens can be
stale by the time it commits (see `NewTxExpressionConstraint`). Paying for that
means `Manager.Execute` runs the whole load → fire → save cycle inside one
database transaction — and *your* code runs inside it too: the `fn` you passed,
the guards, and every event listener they trigger.

The library will not hide that. A listener that makes an HTTP call, or an `fn`
that waits on something, holds a row lock for as long as it takes; on SQLite it
holds the writer. Keep the cycle short and local, or use an ordinary guard and
accept the pre-read. There is no timeout knob, because the right bound is the
one your database already enforces.

## 6. Cross-instance atomicity is a seam, not a feature

Saves are atomic per instance. A step spanning two instances is two
transactions with a crash window between them — the library will not hide
that behind a distributed transaction. The supported pattern is ordered
idempotent writes plus a reconciler case per window (recipe in
[`guides/PRODUCTION_RECIPES.md`](./guides/PRODUCTION_RECIPES.md)); the
token read-model (`Manager.ListPlaceTokens`) exists partly to make those
reconcilers cheap to write.

## 7. No static analyzer (yet)

The Petri-net foundation makes definitions *analyzable in principle*
(soundness, deadlock-freedom, reachability), and `NewDefinition` validates
structure (places exist, resets valid). But there is no shipped
model-checker: a definition that can deadlock is your review's job to
catch today. A static validator is on the roadmap; until it ships, the
README's claim is deliberately "amenable to formal analysis", not
"formally verified".
