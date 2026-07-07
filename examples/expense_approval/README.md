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
`EXPENSE_POSTGRES_DSN`) to run on PostgreSQL instead of SQLite.

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
lands. (Timer firings are the documented exception — see the friction log.)

**Colored tokens where entities flow together.** The payment net
(`payment.yaml`) is the opposite modeling style: ONE long-lived instance,
where every approved expense is a token carrying
`{expense_id, amount, submitter}`. The batch run fires `pay` per token;
the guard `token.amount <= 5000.0` holds big amounts in `payable` for manual
review. Totals on the dashboard come from `AggregateTokens`.

**What the library refuses to do for you** — and this app does explicitly:
terminality on rejection (no cancellation regions yet; `ErrTerminal` in
`app.go`), cross-instance atomicity (expense ↔ payment net; see
`Reconcile`), and side-effect idempotency (at-least-once listeners).

## Tests

```sh
go test -race ./...
```

The tests are the acceptance criteria: escalation via a future-`now` tick
(no sleeping), webhook redelivery dedup, reject terminality, the payment
guard, crash consistency (state+history roll back together), concurrent
approvals under optimistic-concurrency retry, restart resuming the fleet,
and reconcile repairing the documented crash windows.
