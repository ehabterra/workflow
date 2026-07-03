# Timer Escalation Example (host-driven timers)

This example demonstrates the **host-driven timer model** (roadmap M4): the library
*models* time, but the **host owns the clock**. There is no internal scheduler and
no goroutine — a host cron asks "what is due as of now?" and fires it. That is what
makes the whole mechanism restart-safe by construction: the state lives in the
database and the clock lives in the host, so a crash between ticks loses nothing.

The scenario is the canonical one: **escalate an approval if nobody acts within
three days.**

## What it shows

- A YAML workflow with a 3-day timer on a transition (`after: 72h`).
- A fleet of SQLite-persisted instances seeded at **different ages**.
- A ~10-line "cron tick" — `ListDue` → `FireDue` — that advances the whole fleet.
- A **fixed, hand-advanced clock**, so the run is instant and deterministic (no
  sleeping, no wall-clock).
- The two rules that keep timers honest:
  - an already-**approved** instance has no running timer, so it never appears in
    `ListDue` and is never escalated (business action wins over the clock);
  - firing is **idempotent** — once an instance escalates it drops out of `ListDue`.

## The workflow

Loaded from [`workflow.yaml`](workflow.yaml):

```yaml
transitions:
  - name: approve
    from: [submitted]
    to: [approved]
  - name: escalate
    from: [submitted]
    to: [escalated]
    after: 72h   # host-driven timer: due 72h after the token entered `submitted`
```

## The cron tick

The entire host-side machinery is this loop (from [`main.go`](main.go)):

```go
func tick(ctx context.Context, mgr *workflow.Manager, def *workflow.Definition, now time.Time) {
    dueIDs, err := mgr.ListDue(ctx, now, 0) // instances overdue as of `now`
    if err != nil {
        log.Fatalf("ListDue: %v", err)
    }
    for _, id := range dueIDs {
        fired, err := mgr.FireDue(ctx, id, def, now) // advance one instance
        if err != nil {
            log.Printf("FireDue(%s): %v", id, err)
            continue
        }
        fmt.Printf("%s escalated (fired %v)\n", id, fired)
    }
}
```

A real deployment calls `tick` from cron or a `time.Ticker` with `time.Now()`. This
example hands it a fixed clock advanced day by day so the output is reproducible.

## Running the example

```bash
go run ./examples/timer_escalation
```

Expected output:

```
Fleet seeded (deadline = submitted + 72h):
  req-101-fresh     submitted 1 day  ago
  req-102-aging     submitted 2 days ago
  req-103-stale     submitted 4 days ago
  req-104-ancient   submitted 9 days ago
  req-105-approved  submitted 4 days ago, then APPROVED (timer gone)

=== cron tick @ 2026-01-08 09:00 ===
  ListDue reports overdue: [req-104-ancient req-103-stale]
  req-104-ancient  escalated (fired [escalate])
  req-103-stale    escalated (fired [escalate])

=== cron tick @ 2026-01-09 09:00 ===
  ListDue reports overdue: [req-102-aging]
  req-102-aging    escalated (fired [escalate])

=== cron tick @ 2026-01-10 09:00 ===
  ListDue reports overdue: [req-101-fresh]
  req-101-fresh    escalated (fired [escalate])

Nothing left overdue — the fleet has settled. ...
```

Note that `req-105-approved` never appears: approval left the `submitted` place, so
no timer is running. `ListDue` orders by deadline, so the oldest overdue instance
comes first.

## Learn more

See [docs/guides/TIMERS_GUIDE.md](../../docs/guides/TIMERS_GUIDE.md) for the full
mental model, the API tour (`SetTimeoutAfter` / `after:`, `Due` / `NextDue`,
`EnteredAt`, `WithClock`), the AND-join deadline semantics, guard interaction,
tick-frequency guidance, and the multi-host concurrency story.
