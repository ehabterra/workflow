# Dogfood: the expense-approval reference system (M5)

This document specs the M5 reference system — a near-real **expense-approval
service** built *on top of* the library, the way a real host application would.
Its purpose is not to be a product; it is a **friction-log generator**: every
advertised feature must be exercised by real code, and every papercut found
while building it becomes a tracked issue (M5.4). The parked milestones are
re-prioritized from that list, not from speculation.

Decisions (2026-07-07): the system is the roadmap's expense-approval service
**plus a thin server-rendered web UI**, and it lives as a nested Go module at
`examples/expense_approval` (not a separate repo) so it iterates in lockstep
with the library.

## The domain

An employee submits an expense. Legal and Finance review it **in parallel**;
both must approve before the expense is approved. Either may reject. If a
reviewer sits on it for **3 days**, that branch escalates (flagged for a
manager, still approvable). Approved expenses accumulate and are paid out in a
**batch payment run** — the CPN-flavored component, where each approved expense
is a colored token in a single payment net.

## The Petri net (one instance per expense)

Places:

- `draft` — initial
- `pending_legal`, `pending_finance` — parallel review branches (AND-split)
- `escalated_legal`, `escalated_finance` — branch overdue, flagged, still open
- `legal_ok`, `finance_ok` — branch approved
- `approved` — AND-join of both branches
- `rejected` — terminal; either reviewer can reject
- `paid` — set by the batch payment run

Transitions:

| Transition | From | To | Notes |
|---|---|---|---|
| `submit` | `draft` | `pending_legal`, `pending_finance` | AND-split |
| `legal_approve` | `pending_legal` | `legal_ok` | webhook-fired |
| `finance_approve` | `pending_finance` | `finance_ok` | webhook-fired |
| `legal_escalate` | `pending_legal` | `escalated_legal` | `after: 72h` (M4) |
| `finance_escalate` | `pending_finance` | `escalated_finance` | `after: 72h` (M4) |
| `legal_approve_escalated` | `escalated_legal` | `legal_ok` | |
| `finance_approve_escalated` | `escalated_finance` | `finance_ok` | |
| `legal_reject` | `pending_legal` | `rejected` | guard: not already rejected |
| `finance_reject` | `pending_finance` | `rejected` | guard: not already rejected |
| `legal_reject_escalated` | `escalated_legal` | `rejected` | |
| `finance_reject_escalated` | `escalated_finance` | `rejected` | |
| `finalize` | `legal_ok`, `finance_ok` | `approved` | AND-join; auto-fired by the host after either approval |
| `mark_paid` | `approved` | `paid` | fired by the batch payment run |

Known modeling frictions, logged up front (M5.4):

- **No cancellation regions** (parked milestone): when one branch rejects, the
  other branch's token stays live in its `pending_*`/`escalated_*` place. The
  host treats `rejected` as terminal and the UI hides the stale branch; guards
  on the reject transitions stop a *second* reject from double-firing into
  `rejected`. This is exactly the friction the parked "cancellation regions"
  item exists for.
- **Escalation doubles the transition count**: because a transition's inputs
  are fixed places, "approve from pending *or* escalated" needs two transitions
  per action. Workable, verbose; logged.

## The payment net (one net, many tokens — CPN)

A single long-lived instance `payment-batch` with places `payable` → `paid_out`.
When an expense reaches `approved`, the host drops a **token** into `payable`
carrying `{expense_id, amount, submitter}`. The batch run (manual button and/or
cron) fires `pay` per token (`ApplyTransitionForToken`), with a guard rejecting
amounts above a limit, and records the run in history. Token queries/aggregation
power the dashboard's "pending payout total".

## Feature-coverage matrix (why this domain)

| Library feature | Exercised by |
|---|---|
| AND-split / AND-join | `submit`, `finalize` |
| Host-driven timers (M4) | `*_escalate` `after: 72h`; cron tick calls `ListDue`/`FireDue` |
| Webhook-resumed waits | approve/reject endpoints; redelivery answered by `ErrNotEnabled` (idempotent) vs `ErrGuardRejected` (forbidden) — the M3.4 split |
| Fleet of persisted instances | one instance per expense; `ListWorkflowIDs` dashboard |
| Atomic state+history (M3.5) | every fire goes through `Manager.Execute` + `WithTxSideEffect(history.SaveTransitionTx)` |
| Audit trail | history table rendered on the expense detail page |
| Optimistic concurrency | two reviewers clicking simultaneously; `Execute` retry |
| Definition fingerprint | restart with an edited YAML fails loudly (documented recovery) |
| CPN tokens/queries/guards | the payment net |
| Metrics | listener-based firing counters on `/metrics` (plain text now; `contrib/otel` extraction is M5.3) |
| Crash recovery | kill-tests: state and history never disagree; restart resumes the fleet |

## Architecture

```
examples/expense_approval/          (nested Go module, like the other examples)
  main.go          — flags/env, storage + manager wiring, HTTP server, cron tick
  workflow.yaml    — the expense net (strict-loader-valid, after: 72h)
  server.go        — handlers: submit, approve/reject webhooks, batch run, pages
  templates/       — html/template: dashboard, expense detail, submit form
  *_test.go        — fake-clock escalation tests, webhook dedup, kill/crash test
```

- **Storage**: SQLite by default (zero-setup demo); Postgres when
  `EXPENSE_POSTGRES_DSN` is set — same both-strategy as the conformance suite.
- **Clock**: the host owns it. A `time.Ticker` (default 10s, flag-tunable)
  calls `Manager.ListDue` + `FireDue`. Tests inject a fake clock through the
  same code path — no sleeping.
- **UI**: server-rendered `html/template`, no JS build step. Pages: fleet
  dashboard (states + next-due), expense detail (marking, history, actions),
  submit form, payment batch page.
- **No bespoke layers**: the exit criterion. If this app needs its own
  persistence or scheduling layer to be buildable, that finding outranks any
  roadmap item.

## Friction-log protocol (M5.4)

Every papercut goes to `docs/roadmap/FRICTION.md` as it is found — one line:
what was attempted, what the library made easy/hard/impossible, workaround.
Graduated to GitHub issues at the end of M5.1. The M5 exit verdict summarizes
the list and re-ranks the parked milestones against it.
