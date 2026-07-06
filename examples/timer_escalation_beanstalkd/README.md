# Timer Escalation across replicas with Beanstalkd

This example extends the [`timer_escalation`](../timer_escalation) demo to a
**multi-replica** deployment, using [Beanstalkd](https://beanstalkd.github.io/)
purely as a **job distributor**. The library still owns no clock and no scheduler
— it only answers "what is due as of now?" (`ListDue`) and "fire it" (`FireDue`).
Beanstalkd sits between those two calls and hands each due instance to whichever
worker is free, so **at most one worker successfully fires each instance** — the
broker spreads the work, and `FireDue`'s idempotency makes any redelivery a no-op.

The scenario is the same as the base demo: **escalate an approval if nobody acts
within three days** — but now the firing work is fanned out across a pool of
long-lived competing workers, and the demo deliberately injects the two failure
modes a distributor introduces, to show both are absorbed:

- **a duplicate delivery** (the same instance enqueued twice, as if two scans
  overlapped) — one worker escalates it, the other finds nothing due and no-ops;
- **a worker crash mid-job** (reserved but never completed) — the job survives
  its worker and is redelivered to another, which completes the escalation.

## Architecture

```text
ListDue (SQLite/Postgres) ──▶ dispatcher (singleton) ──▶ "timers" tube
                                                              │
                            reserve (competing) ◀─────────────┘
                            FireDue + delete
                            [long-lived worker replicas]
```

- The **database is the source of truth** for *what* is due. `ListDue` is an
  indexed `WHERE due <= now` scan.
- **Beanstalkd only decides *who* fires it.** A tube is a work queue; a reserved
  job is held for one worker at a time, so under normal operation each worker
  gets a *disjoint* set — that is the fan-out that spreads the load.
- **The job payload carries the scan time** (`{"id": ..., "now": ...}`), so a
  worker fires *as of the dispatcher's decision time* — deterministic even if the
  job waits in the queue. (A deployment may instead call `time.Now()` in the
  worker; both are sound.)
- `FireDue` rides optimistic concurrency, so if a job is ever redelivered (a
  worker crash, a TTR expiry) or double-enqueued, the second firing is a no-op.
  Correctness rests on this idempotency, not on the broker guaranteeing
  exactly-once delivery.

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
- A **singleton dispatcher** (`ListDue` → `put`) plus a pool of **long-lived
  competing workers** (`reserve` → `FireDue` → `delete`), each on its own
  Beanstalkd connection — stand-ins for separate replicas. Workers know nothing
  about "ticks"; they just drain the tube, forever.
- **Both distributor failure modes demonstrated live**, not just documented:
  the duplicate delivery no-ops, and the crashed worker's job is completed by a
  peer.
- A **fixed, hand-advanced clock**, so the run is deterministic and never sleeps
  for real time.

## Running the example

Beanstalkd must be reachable first. A one-service compose file is included:

```bash
cd examples/timer_escalation_beanstalkd
docker compose up -d --build   # start beanstalkd on :11300
go run .                       # seed a fleet and drive the cron
docker compose down            # stop it when done
```

Point at a different broker with `BEANSTALKD_ADDR=host:port`.

Expected output (worker numbers and line order within a tick vary — the point is
every instance ends up escalated exactly once):

```text
Fleet seeded (deadline = submitted + 72h):
  req-101-fresh     submitted 1 day  ago
  ...
Distributor: beanstalkd @ 127.0.0.1:11300, tube "timers", 3 long-lived competing workers

=== cron tick @ 2026-01-08 09:00 ===
  ListDue reports overdue: [req-104-ancient req-103-stale]
  (enqueued req-104-ancient TWICE to simulate an overlapping scan)
  [worker 1] req-104-ancient  escalated (fired [escalate])
  [worker 3] req-103-stale    reserved... simulated CRASH mid-job (job survives, will be redelivered)
  [worker 2] req-104-ancient  nothing due — duplicate delivery, no-op
  [worker 1] req-103-stale    escalated (fired [escalate])

=== cron tick @ 2026-01-09 09:00 ===
  ListDue reports overdue: [req-102-aging]
  [worker 1] req-102-aging    escalated (fired [escalate])
...
Nothing left overdue — the fleet has settled.
```

The crash releases the job with zero delay to keep the demo instant; in a real
crash the same thing happens when the job's **TTR** expires. The demo worker
keeps running afterward — only that job's handling "died".

## Taking it to production

The demo runs every role in one process. In a real deployment:

- **The dispatcher must be a singleton.** Beanstalkd has no leader election, so
  if every replica scans you are back to N× DB load and duplicate `put`s. Run the
  dispatcher as a single pod / k8s `CronJob`, or gate it behind a Postgres
  advisory lock (`pg_advisory_lock`) so only the lock-holder scans. **Workers run
  everywhere.**
- **Lean on TTR for crash-safety.** Each job is `put` with a time-to-run (`jobTTR`
  here). If a worker reserves a job and dies before `delete`, Beanstalkd
  auto-releases it after TTR for another worker — exactly what the simulated
  crash demonstrates. Set it above your `FireDue` latency and call `Touch` if a
  firing ever runs long.
- **The drain-wait is demo bookkeeping.** The dispatcher here polls tube stats so
  each tick's output prints together; production workers just run forever and
  nobody waits for a "tick" to finish.
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
