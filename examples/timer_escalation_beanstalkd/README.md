# Timer Escalation across replicas with Beanstalkd

This example extends the [`timer_escalation`](../timer_escalation) demo to a
**multi-replica** deployment, using [Beanstalkd](https://beanstalkd.github.io/)
purely as a **job distributor**. The library still owns no clock and no scheduler
— it only answers "what is due as of now?" (`ListDue`) and "fire it" (`FireDue`).
Beanstalkd sits between those two calls and hands each due instance to whichever
worker is free, so **no two replicas ever fire the same instance**.

The scenario is the same as the base demo: **escalate an approval if nobody acts
within three days** — but now the firing work is fanned out across a pool of
competing workers.

## Architecture

```text
ListDue (SQLite/Postgres) ──▶ dispatcher (singleton) ──▶ "timers" tube
                                                              │
                            reserve (competing) ◀─────────────┘
                            FireDue + delete
                            [worker replicas]
```

- The **database is the source of truth** for *what* is due. `ListDue` is an
  indexed `WHERE due <= now` scan.
- **Beanstalkd only decides *who* fires it.** A tube is a work queue; multiple
  workers reserving from it each get a *disjoint* set of jobs — that is the
  fan-out that prevents duplicate work.
- `FireDue` rides optimistic concurrency, so even if a job is somehow delivered
  twice, the second firing is a no-op.

### Why a simple, in-memory distributor is enough

Because `ListDue` **re-derives** the due set on every tick, the distributor does
**not** need to be durable or exactly-once:

- a **dropped** job is simply re-listed on the next tick (the DB still holds the
  deadline);
- a **duplicated** job is a no-op (`FireDue`'s version check wins once).

That is what lets you use the smallest possible tool. Beanstalkd is a single tiny
binary whose only job is to distribute work — no persistence to operate, no
exactly-once machinery to pay for.

## What it shows

- A YAML workflow with a 3-day timer (`after: 72h`).
- A fleet of SQLite-persisted instances seeded at **different ages**.
- A **singleton dispatcher** (`ListDue` → `put`) plus a pool of **competing
  worker goroutines** (`reserve` → `FireDue` → `delete`), each on its own
  Beanstalkd connection — stand-ins for separate replicas.
- A **fixed, hand-advanced clock**, so the run is deterministic and never sleeps
  for real time.
- Visible fan-out: in a tick with two overdue instances, two *different* workers
  each take one.

## Running the example

Beanstalkd must be reachable first. A one-service compose file is included:

```bash
cd examples/timer_escalation_beanstalkd
docker compose up -d --build   # start beanstalkd on :11300
go run .                       # seed a fleet and drive the cron
docker compose down            # stop it when done
```

Point at a different broker with `BEANSTALKD_ADDR=host:port`.

Expected output (worker numbers vary — the point is each instance is handled by
exactly one worker):

```text
Fleet seeded (deadline = submitted + 72h):
  req-101-fresh     submitted 1 day  ago
  ...
Distributor: beanstalkd @ 127.0.0.1:11300, tube "timers", 3 competing workers

=== cron tick @ 2026-01-08 09:00 ===
  ListDue reports overdue: [req-104-ancient req-103-stale]
  [worker 2] req-104-ancient  escalated (fired [escalate])
  [worker 3] req-103-stale    escalated (fired [escalate])
  tick done: 2 escalated
...
Nothing left overdue — the fleet has settled.
```

## Taking it to production

The demo runs every role in one process. In a real deployment:

- **The dispatcher must be a singleton.** Beanstalkd has no leader election, so
  if every replica scans you are back to N× DB load and duplicate `put`s. Run the
  dispatcher as a single pod / k8s `CronJob`, or gate it behind a Postgres
  advisory lock (`pg_advisory_lock`) so only the lock-holder scans. **Workers run
  everywhere.**
- **Lean on TTR for crash-safety.** Each job is `put` with a time-to-run (`jobTTR`
  here). If a worker reserves a job and dies before `delete`, Beanstalkd
  auto-releases it after TTR for another worker. Set it above your `FireDue`
  latency and call `Touch` if a firing ever runs long.
- **Prefer the scan (this example) over per-instance delayed jobs.** Beanstalkd
  *can* delay a job (`put` with a delay = time until the deadline), but it holds
  **every job in memory** — a fleet of millions of multi-day timers would be a lot
  of RAM. Let the database hold deadlines (it is built for that) and use
  Beanstalkd only to distribute the *ready-now* work.
- **Use Postgres, not SQLite, for real replicas.** SQLite is single-writer; this
  demo sets `SetMaxOpenConns(1)` to serialize writes and avoid lock flakes. The
  Postgres backend handles concurrent writers natively.

## Learn more

See [docs/guides/TIMERS_GUIDE.md](../../docs/guides/TIMERS_GUIDE.md) for the full
timer model, and the base [`timer_escalation`](../timer_escalation) example for the
single-process version without a distributor.
