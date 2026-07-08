# Timers (host-driven time)

The single most-demanded real-world primitive is *"escalate if not approved in
three days."* This library supports it — but deliberately **without** an internal
clock, scheduler, or goroutine. Instead:

> **The library models time; the host owns the clock.**

A definition *declares* durations, tokens *carry* the times they entered their
places, and `Due(now)` is a **pure function** of the marking and a `now` you supply.
Nothing fires on its own; a host (cron, ticker, or work queue) decides when to
evaluate deadlines and fire what is due.

There is a runnable end-to-end version of everything below in
[`examples/timer_escalation`](../../examples/timer_escalation).

## The mental model

Three pieces, each doing one job:

| Piece | What it holds | API |
| --- | --- | --- |
| **Definition** | the *duration* — "escalate 72h after entry" | `SetTimeoutAfter` / YAML `after:` |
| **Token** | *when* it entered its current place | `Token.EnteredAt()` |
| **Evaluation** | the *deadline*, computed on demand from a supplied `now` | `Due(now)` / `NextDue()` |

The deadline is never stored on the transition — it is `entryTime + after`, computed
per instance whenever you ask. The same definition therefore drives every instance's
timer, and the answer to "is this due?" is a deterministic function of persisted
state plus the `now` the host passes in. That is what makes the whole model
unit-testable without ever sleeping.

## The boundary: no internal scheduler

This is boundary #2 of the library's [honest boundary statements](../../ROADMAP.md#boundaries):

> **No internal clock or scheduler.** Time enters via the host.

There are no background goroutines and nothing polls a clock inside the library. A
timed transition becomes *eligible* to fire once its deadline passes, but it only
actually fires when a host calls `FireDue`. The consequences are all upside for an
embedded library:

- **Restart-safe by construction.** The deadline is derived from persisted token
  entry times, not from an in-memory timer. A crash between ticks loses nothing:
  when the host comes back up and ticks again, the same instances are still overdue
  and get fired. Recovery is just *load and re-drive*.
- **Deterministic and testable.** Because `now` is always an explicit parameter,
  you test "what happens after three days" by passing a `now` three days later — no
  fake time packages, no sleeping, no flakiness.
- **No hidden concurrency.** The library never fires a transition on a goroutine you
  didn't start, so it can't race your own code or surprise you with a side effect at
  an arbitrary moment.

**Contrast with durable-execution engines.** Systems like Temporal or AWS Step
Functions are excellent at a different job: they persist *the code's execution
state* and own a scheduler that wakes a workflow up when a timer fires. That is a
powerful model and the right choice when you want to write long-running orchestration
as straight-line code. This library makes the opposite trade on purpose: it is an
in-process library that persists *state, not code*, and leaves the clock to you. You
bring a cron; it brings correctness under concurrency. Neither is "better" — they
solve different problems.

## API tour

### Declaring a timer

In Go, mark a transition time-driven with a positive duration:

```go
escalate := workflow.MustNewTransition("escalate",
    []workflow.Place{"submitted"}, []workflow.Place{"escalated"})
escalate.SetTimeoutAfter(72 * time.Hour) // due 72h after a token enters `submitted`
```

Or declaratively in YAML with `after:` (any `time.ParseDuration` string — `30m`,
`24h`, `72h`):

```yaml
transitions:
  - name: escalate
    from: [submitted]
    to: [escalated]
    after: 72h
```

`TimeoutAfter()` reads the duration back — it lives on the transition itself as the
single source of truth (there is no separate `after` metadata mirror), and the
diagram renderer derives the timer label from it directly. A non-positive duration
clears the timer. A timer-free workflow is completely unaffected — entry-time
stamping only kicks in for definitions that actually declare a timer, so nothing
changes on the wire for everyone else.

### Asking what is due

`Due` and `NextDue` are methods on a live `*Workflow`. `now` is always explicit:

```go
// Everything overdue as of `now` — timed AND currently enabled transitions.
for _, t := range wf.Due(now) {
    fmt.Println("overdue:", t.Name())
}

// The earliest deadline across all running timers (false if none is running).
if deadline, ok := wf.NextDue(); ok {
    fmt.Println("next deadline:", deadline)
}
```

`Due` returns only transitions that are **timed and currently enabled** (every input
place occupied by a stamped token). Guards are *not* evaluated here — `Due` reports
"the clock says go"; whether the transition may actually fire is decided at firing
time (see [Guards](#guards-dont-lose-to-the-clock-and-vice-versa)).

### Reading a token's entry time

```go
for _, tok := range wf.GetTokens("submitted") {
    if at, ok := tok.EnteredAt(); ok {
        fmt.Println("waiting since", at)
    }
}
```

`EnteredAt` returns `ok == false` for tokens in timer-free workflows and for state
persisted before timer support existed (see
[pre-timer state](#pre-timer-persisted-state)).

### A fixed clock for tests (and demos)

`WithClock` pins the clock the engine uses to stamp tokens as they enter a place, so
seeded entry times — and therefore deadlines — are exact:

```go
clock := func() time.Time { return t0 } // constant clock
wf, _ := workflow.NewWorkflow("req-1", def, "submitted", workflow.WithClock(clock))
```

Combined with an explicit `now` in `Due`/`FireDue`, this makes every timer scenario
reproducible without touching the wall clock.

## The fleet recipe

A live `Due` call is fine for one in-memory instance, but the real use case is a
*fleet* of persisted instances: "across every request in the database, escalate the
ones that have waited too long." Two pieces make that a ~10-line cron.

### 1. Schema setup

Timers need a maintained **due index** — one column per instance holding its
next-due time (SQL `NULL` when no timer is running). The SQLite and Postgres
backends provide it. Call `EnsureSchema` once on startup; it creates the table, adds
the due column and its index, and idempotently migrates a pre-existing table:

```go
store, _ := storage.NewSQLiteStorage(db) // due column is on by default ("due_at")
if err := store.EnsureSchema(ctx); err != nil {
    log.Fatal(err)
}
mgr := workflow.NewManager(workflow.NewRegistry(), store)
```

The Manager keeps the index current on **every** save path (`SaveWorkflow`,
`CreateWorkflow`, `Execute`, `FireDue`), so it can never drift from the stored
marking. To disable the index on a table you cannot migrate, pass
`storage.WithDueColumn("")` — the backend then stops advertising `DueStorage` and a
fleet scan is simply unavailable for it.

> **Composing transactions by hand?** Storage-level saves do **not** maintain the
> due index: `SaveState`, `SaveStateTx`, and `SaveStateInTx` all leave the due
> column untouched. If you drive a timed
> definition's saves manually (e.g. inside `RunInTx`), use
> `SaveStateWithDue` / `SaveStateInTxWithDue` — or just go through
> the Manager — so the index commits atomically with the state change.

### 2. The cron loop

Find the overdue instances, then advance each one:

```go
func tick(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, now time.Time) {
    dueIDs, err := mgr.ListDue(ctx, now, 0) // IDs whose deadline <= now, earliest first
    if err != nil {
        log.Fatal(err)
    }
    for _, id := range dueIDs {
        fired, err := mgr.FireDue(ctx, id, def, now) // fire every due transition on this instance
        if err != nil {
            log.Printf("FireDue(%s): %v", id, err)
            continue
        }
        if len(fired) > 0 {
            log.Printf("%s: fired %v", id, fired)
        }
    }
}
```

Schedule `tick` from cron or a `time.Ticker` with `time.Now()`. `ListDue`'s `before`
is the authoritative clock for the whole scan, and `FireDue`'s `now` pins the clock
that stamps any tokens the firing produces — pass the same value so downstream
deadlines are measured from one consistent instant.

`ListDue(ctx, before, limit)` orders by deadline ascending; a `limit` of 0 means no
limit. To page a very large backlog, drain a batch with `FireDue` before rescanning,
or raise `before` in steps.

### Exactly-once audit records for timer firings

To record each firing in a history/audit table, don't write it after `FireDue`
returns — a crash between the state commit and your write loses the record. Use
`WithFireDueTxSideEffect`, which runs *inside* the save's transaction with the
fired steps in hand (transition plus the marking on each side), so the state
change and its record commit or roll back together:

```go
fired, err := mgr.FireDue(ctx, id, def, now,
    workflow.WithFireDueTxSideEffect(func(ctx context.Context, tx any, steps []workflow.FiredStep) error {
        for _, s := range steps {
            if err := hist.SaveTransitionTx(ctx, tx.(*sql.Tx), &history.TransitionRecord{
                WorkflowID: id, Transition: s.Transition, Actor: "timer", CreatedAt: now,
            }); err != nil {
                return err
            }
        }
        return nil
    }))
```

The effect is skipped when the pass fires nothing, and it requires a
`TransactionalStorage` backend (the SQLite and Postgres backends qualify).

### Guards don't lose to the clock (and vice versa)

A timer says *when* a transition may fire; a **guard** still says *whether* it may.
`FireDue` honors guards: a due transition whose guard rejects it is **skipped, not an
error**, and the instance is left untouched so a later tick can retry it once the
guard opens. Timers never override your business rules.

```go
// escalate is due at 72h, but only fires when the guard allows it:
guard, err := workflow.NewExpressionConstraint(`getContext('on_hold', false) == false`)
if err != nil {
    log.Fatal(err)
}
escalate.AddConstraint(guard)
```

At a tick where `on_hold` is true, `FireDue` reports nothing fired and the instance
stays put; flip `on_hold` to false and the next tick escalates it. (Symmetrically, a
human action that *leaves* the timed place before the deadline — e.g. `approve` — ends
the timer entirely: the instance drops out of `ListDue` and is never escalated.)

### AND-join deadlines run from the *latest* input

When a timed transition joins several input places (an AND-join), it is only
*enabled* once **all** its inputs are occupied — so its deadline runs from the
**latest** input's entry time, not the earliest:

```text
legal_ok ──┐
           ├── finalize (after: 24h)
finance_ok ┘
```

If `legal_ok` was stamped on Monday and `finance_ok` on Wednesday, `finalize`'s 24h
deadline is measured from **Wednesday** — the moment the transition actually became
enabled. A transition with any unoccupied input has no running deadline at all.

### Pre-timer persisted state

Timer support stamps entry times only going forward. A token persisted **before** you
added a timer (or by an older library version) has no entry time, and the model
treats such a place as having an *unknown* start: a transition with any unstamped
input is **never retroactively due**. This is deliberate — an old instance is not
suddenly declared three days overdue the moment you deploy timers. It gets a fresh,
honest deadline the next time a firing produces a stamped token into the relevant
place.

## Choosing a tick frequency

Your tick interval is the granularity of your deadlines: escalations fire on the
first tick *after* the deadline passes, so a 5-minute cron honors a 72h deadline to
within 5 minutes. Match the interval to the tightest timer you care about, not the
loosest — a fleet with a 30-minute timer wants a sub-30-minute tick; one with only
multi-day timers is happy with an hourly or even daily cron.

If you would rather **schedule than poll**, `NextDue` gives you the earliest upcoming
deadline for a single instance, so you can arm a timer for exactly that moment
instead of scanning on a fixed interval:

```go
if deadline, ok := wf.NextDue(); ok {
    time.AfterFunc(time.Until(deadline), func() { /* re-evaluate this instance */ })
}
```

For a whole fleet, the same idea applies at the storage layer: after a scan, query
the minimum `due_at` to decide when the next scan is worthwhile. Polling is simpler
and usually plenty; reach for scheduling only when ticks are expensive or deadlines
are tight.

## Concurrency: multiple cron hosts are safe

You do not need a leader-elected singleton cron. `FireDue` runs under the same
optimistic-concurrency retry loop as `Manager.Execute`: it loads fresh state, fires,
and saves guarded by the instance's version. If two hosts scan the same fleet and
both call `FireDue` on the same instance, the first save wins and the second reloads
to find the transition already fired — so it fires nothing. `FireDue` is idempotent:
once nothing is overdue it does nothing, and an instance with no running timer drops
out of `ListDue` entirely. Run as many cron replicas as you like.

## Summary

- Declare durations with `SetTimeoutAfter` / `after:`; the deadline is
  `entryTime + duration`, computed on demand.
- `Due(now)` / `NextDue()` are pure functions of the marking and a `now` **you**
  supply — the clock is always the host's.
- For a persisted fleet: `EnsureSchema` once, then a `ListDue` → `FireDue` cron.
- Guards still gate firing; AND-joins deadline from the latest input; pre-timer
  tokens are never retroactively due.
- No internal scheduler, no goroutines — restart-safe by construction, and every
  cron replica is safe to run.

See also: [CPN_GUIDE.md](CPN_GUIDE.md) for the token model that entry-time stamping
builds on, and [`examples/timer_escalation`](../../examples/timer_escalation) for the
runnable escalation recipe.

## Cancelling timers with reset arcs

A timer lives on a token; clear the token and the timer dies with it. A
transition's reset arcs (`resets: [pending_finance]` in YAML,
`SetResets(...)` in Go) empty the declared places atomically when it fires,
so a reject/cancel transition can cancel a sibling branch's pending work —
and its escalation deadline — in the same firing. The due index is derived
from the marking on every save, so the instance drops out of `ListDue`
without any host bookkeeping.
