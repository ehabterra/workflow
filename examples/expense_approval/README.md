# Expense approval — the dogfood reference system (M5)

A near-real expense-approval web service built on the workflow library the
way a host application would build it. This README doubles as the mental-model
tutorial: each section names the library concept it demonstrates.

Spec and design decisions: [`docs/DOGFOOD.md`](../../docs/DOGFOOD.md).
Papercuts found while building it: [`docs/roadmap/FRICTION.md`](../../docs/roadmap/FRICTION.md).

## Run it

```sh
go run . -db expenses.db -escalate-after 2m -tick 10s
open http://localhost:8080
```

`-escalate-after 2m` shrinks the production 72h review deadline so you can
watch escalation happen live (safe for existing data: timeouts are not part
of the definition fingerprint). Use `-postgres-dsn` (or
`EXPENSE_POSTGRES_DSN`) to run on PostgreSQL instead of SQLite. Point
`-otel-endpoint host:4318` (or `EXPENSE_OTEL_ENDPOINT`) at an OTLP/HTTP
collector to export a `workflow.fire` span per firing and the
`workflow.firings` counter, via [`contrib/otel`](../../contrib/otel/README.md).

The process is production-shaped where it matters: HTTP timeouts, a request
log, `GET /healthz` for probes, graceful shutdown on Ctrl-C/SIGTERM (a hard
kill is also safe — state lives in the database), a periodic reconcile pass
(`-reconcile`, default 1m) that self-heals the documented crash windows, and
an escalation tick that survives one broken instance without starving the
rest of the fleet. It has **no authentication or CSRF protection** — it is a
reference system for the workflow library, not a deployable product.

Or drive it from the terminal:

```sh
# submit → the Location header carries the new instance ID
curl -si -X POST localhost:8080/expenses \
  -d "submitter=alice&description=team dinner&amount=240.75" | grep -i location

# parallel review: approve one branch, reject is analogous
curl -X POST localhost:8080/expenses/<id>/approve -d "branch=legal&actor=lawyer"

# redeliver the same webhook: a 200 no-op, never a double fire
curl -X POST localhost:8080/expenses/<id>/approve -d "branch=legal&actor=lawyer"

# pay out everything approved (amounts over 5000 are held by the guard)
curl -X POST localhost:8080/batch/run
```

## The mental model, one concept at a time

**One instance per entity.** Every expense is its own workflow instance
(`exp-1a2b3c4d`), persisted as a row: marking + context + version. The
dashboard is just `ListWorkflowIDs` + `LoadWorkflow` over the fleet.

**AND-split / AND-join are just arcs.** `submit` has two `to` places — firing
it puts a token in *both* review branches (parallelism without goroutines:
it's state, not threads). `finalize` has two `from` places — it is enabled
only when *both* branches have approved. See `workflow.yaml`; there is no
special "parallel gateway" concept.

**Time is modeled, never scheduled.** `legal_escalate` carries `after: 72h`.
The library only *records* when tokens entered a place and answers "what is
due at time T?" — the ticker in `main.go` owns the clock and calls
`Manager.ListDue` + `FireDue`. Kill the process and restart it: deadlines
survive, because they live in the database, not in goroutines. Tests pass a
future `now` instead of sleeping.

**Webhooks resume waits, and the error tells you why not.** An instance in
`pending_legal` is simply a row at rest; the approve endpoint fires the
transition when the webhook arrives. The error split does the HTTP mapping:
`ErrNotEnabled` → redelivered webhook, 200 no-op; `ErrGuardRejected` → 403;
`ErrConflict` → retried automatically by `Manager.Execute`.

**State and audit trail commit together.** Every interactive fire goes
through `Manager.Execute` with `WithTxSideEffect` writing the history record
in the *same transaction* as the state save (`app.go: fire`). The
crash-consistency test injects a failing side effect and proves neither half
lands. Timer firings get the same guarantee via `WithFireDueTxSideEffect`:
the effect receives the fired steps (transition + from/to marking) *inside*
`FireDue`'s transaction, so the escalation tick's history records are
exactly-once too — this was friction entry #4 until the library grew the
option.

**Guards route, straight from context.** `submit` is an XOR-split decided
by guard expressions over the workflow context: `amount <= 100.0` sends
petty cash straight to `approved` (`submit_auto`), anything else into
parallel review. No engine primitive picks the route — the host tries each
variant and treats a guard rejection as "not this path" (`routeSubmit`),
which is friction entry #3.

**Cycles are just arcs too.** `revise` closes the loop: a rejected expense
returns to `draft`, takes an updated amount, and re-routes through the
submit guards — possibly straight to auto-approval.

**One transition per action, whatever the stage.** `legal_approve` accepts
from `pending_legal` OR `escalated_legal` (`from_any: true` — an OR-input
merge that consumes only the marked stage), so escalation no longer doubles
the transition count. And the submit XOR-split is resolved by the library:
`ApplyAny("submit_auto", "submit")` fires whichever route the amount guard
allows.

**The diagrams cannot drift.** `/diagrams`, the expense pages, and the
batch page render the nets with the library's own Mermaid renderer
(`Definition.Diagram` / `Workflow.Diagram`): entry markers, transition
nodes color-typed by who fires them (declared per transition with the
`diagram_class` metadata — person / automatic; ⏱ timers are derived),
guards visible on the routing edges, dotted "cancels" reset arcs,
splits and merges routed through gateway diamonds with the BPMN
symbols — `◇+` (parallel: every branch) or `◇×` (exclusive/OR-input:
exactly one) — the two parallel review lanes boxed as `subgraph`
regions (via a `diagram_group` place metadata), the live marking
highlighted, and ⬤×N token badges on colored-token places — with a
legend for non-technical readers.

**Rejection cancels the sibling branch — declaratively.** Each reject
transition carries reset arcs (`resets: [pending_finance, …]`): firing it
clears the other branch's places atomically, killing its pending token and
any escalation timer running on it. This replaced the host-side `ClearPlace`
token surgery that was the friction log's top-ranked finding — the library
grew the feature and the app deleted the workaround.

**Colored tokens where entities flow together.** The payment net
(`payment.yaml`) is the opposite modeling style: ONE long-lived instance,
where every approved expense is a token carrying
`{expense_id, amount, submitter}`. The batch run fires `pay` per token; the
guard `token.amount <= 5000.0` holds big amounts in `payable`, and a
reviewer pays those out individually with the `release` transition (manual
review, reviewer recorded in the audit trail). Totals come from
`AggregateTokens`. The net starts **empty** — a marking with zero places is
a valid persisted state (`CreateWorkflowFromMarking` with an empty marking),
so no always-marked anchor place is needed and tokens exist only for
expenses actually flowing through.

**Definitions evolve; the fingerprint notices — and the diff says what.**
Adding `release`/`revise` changed both nets' fingerprints, so the app
installs a `WithDefinitionMigration` hook. The mismatch now carries a
structural `DefinitionDiff` (places/transitions added, removed, changed —
by name), so approval is a policy instead of blind trust:
`Diff.Additive()` changes pass mechanically, non-additive expense-net
changes are refused, and the loader still re-validates every loaded place
afterwards. Deleting the payment net's `batch_control` anchor place is
the one change approval alone can't cover: the hook runs real migration
logic (`migratePaymentAnchor`), stripping the anchor from the stored
marking before the manager reloads.

**What the library refuses to do for you** — and this app does explicitly:
terminality on rejection (no cancellation regions yet; `ErrTerminal` in
`app.go`), cross-instance atomicity (expense ↔ payment net; see
`Reconcile`), and side-effect idempotency (at-least-once listeners).

## Tests

```sh
go test -race ./...
```

The tests are the acceptance criteria: escalation via a future-`now` tick
(no sleeping), webhook redelivery dedup, reject terminality, guard-routed
auto-approval and its exact boundary, the revise/resubmit cycle (including
the stranded-token surgery and a quiet due index afterwards), the payment
guard plus manual release (idempotent, audited), crash consistency
(state+history roll back together), concurrent approvals under
optimistic-concurrency retry, restart resuming the fleet, reconcile
repairing the documented crash windows (including deleting creation-crash
drafts), the stranded-branch escalation staying harmless after a rejection,
tick-vs-approve and batch-vs-batch races, and input validation (`Inf`/`NaN`
amounts never reach storage).

Set `EXPENSE_POSTGRES_DSN` to also run the lifecycle against PostgreSQL
(CI does; it drops and recreates the app's tables — use a scratch
database).
