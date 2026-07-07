# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Dogfood reference system (M5.1): `examples/expense_approval`, a near-real
  expense-approval web service exercising every advertised feature — parallel
  legal+finance review (AND-split/join), 72h host-driven escalation timers with
  a ticker calling `ListDue`/`FireDue`, webhook-resumed waits with the
  `ErrNotEnabled`/`ErrGuardRejected` split mapped to HTTP semantics, a fleet of
  SQL-persisted instances (SQLite or PostgreSQL), a crash-consistent audit
  trail via `Execute` + `WithTxSideEffect`, listener-based firing metrics, a
  server-rendered UI, and a CPN batch payment net where approved expenses flow
  as colored tokens held back by an amount guard. Its README doubles as the
  mental-model tutorial; `docs/DOGFOOD.md` holds the spec.
- M5 friction log (M5.4, ongoing): `docs/roadmap/FRICTION.md` records every
  papercut the dogfood build hit (cancellation regions, OR-input transitions,
  empty-marking persistence, `FireDue` history atomicity, cross-instance
  recipes) and what the library made easy.

## [0.8.0] - 2026-07-07

Rolls up all work since v0.0.2: storage truth & hardening (M0), crash-safe ACID
storage (M1), Colored Petri Nets (M2), real-system correctness (M3), and
host-driven timers (M4).

### Added
- Release automation: pushing a `vX.Y.Z` tag re-runs the race-enabled test suite
  against the tagged commit, extracts that version's section from this file, and
  publishes a GitHub Release (`.github/workflows/release.yml`). Tagging without a
  matching changelog section fails the release.
- Crash-safe persistence (M1.1): `storage.RunInTx` plus `SaveStateTx`/`LoadStateTx`/
  `DeleteStateTx` on the SQLite storage and `SaveTransitionTx` on the SQLite history
  store. Callers can now commit a workflow state change and its history record in a
  single transaction, so a crash mid-transition can no longer leave the state and the
  audit log disagreeing.
- Optimistic concurrency (M1.2): new optional `workflow.VersionedStorage` interface
  (`LoadVersionedState`/`SaveVersionedState`) and `workflow.ErrConflict` sentinel.
  `SQLiteStorage` implements it (via a new `version` column), and the `Manager` uses
  it automatically when the backend supports it — two writers racing to save the same
  workflow no longer silently clobber each other; the stale save returns `ErrConflict`.
  `Workflow.Version()` exposes the current instance version.
- PostgreSQL backend (M1.3): `storage.NewPostgresStorage` implements `Storage` and
  `VersionedStorage` using PostgreSQL syntax (`$N` placeholders, `INSERT ... ON
  CONFLICT` upserts). It works with any PostgreSQL database/sql driver; the pgx v5
  stdlib adapter is recommended. It passes the same conformance suite as SQLite,
  proving the storage interface is not SQLite-shaped.
- Storage conformance kit (M1.4): new `storagetest` package with
  `storagetest.Run(t, factory)` that any `workflow.Storage` backend can run to verify
  it honors the Storage contract (and the versioning contract, when supported). Both
  the SQLite and PostgreSQL backends run it.
- `ROADMAP.md` describing the phased path (M0–M8) to a production-ready v1.0.
- Strict YAML decoding: unknown keys in a workflow config are now rejected with a
  line number instead of being silently ignored.
- `.golangci.yml` and a golangci-lint CI job.
- CI job that builds and vets every example module (including nested-module examples).
- `context.Context` is now the first parameter of every `Storage`, `HistoryStore`,
  and `Manager` persistence method; the SQLite implementations honor cancellation.
- Community files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, and
  GitHub issue/PR templates.
- Full context persistence (M3.1): `Storage.SaveState`/`LoadState` now round-trip the
  entire workflow context (not just declared custom-field columns) via an always-on
  JSON `context` column (`JSONB` on PostgreSQL). Configurable with
  `storage.WithContextColumn`.
- Specific blocked-transition errors (M3.3): `workflow.ErrNotEnabled` (a required place
  is unmarked) and `workflow.ErrGuardRejected` (a guard returned false) replace the
  generic `ErrTransitionNotAllowed` on the apply/can paths. Both still satisfy
  `errors.Is(err, ErrTransitionNotAllowed)`, so existing checks keep working.
- Definition safety (M3.3): `Definition.Fingerprint()` returns a stable, collision-free
  SHA-256 of the net's structure (length-prefixed serialization). The `Manager` stamps
  it into saved context and, on load, rejects a persisted instance whose definition no
  longer matches with `workflow.ErrDefinitionMismatch`; `WithDefinitionMigration` lets
  a host migrate or approve mismatched state instead.
- Atomic execute helper (M3.5): `Manager.Execute(ctx, id, def, fn, opts...)` loads,
  mutates, and saves an instance under optimistic concurrency, retrying on `ErrConflict`
  with jittered backoff (`WithMaxRetries` to tune). `WithTxSideEffect` commits a state
  change and a side effect (e.g. a history record) in one transaction on backends that
  implement the new optional `workflow.TransactionalStorage` interface.
- Paginated instance listing (M3.6): optional `workflow.ListableStorage` interface with
  `ListIDs(ctx, ListOptions{Limit, Offset})`, implemented by the SQLite and PostgreSQL
  backends and covered by the conformance suite; `Manager.ListWorkflowIDs` surfaces it.
- Registry cache control (M3.8): `Manager.EvictWorkflow` and `WithoutRegistryCache` for
  hosts that need every load to re-read from storage.
- Dialect-aware SQL history: `history.NewPostgresHistory` alongside
  `history.NewSQLiteHistory`, both from a shared `SQLHistory` implementation.
- Host-driven timers (M4): the library *models* time while the host owns the clock —
  no goroutines, no internal scheduler. Tokens record when they entered a place
  (`enteredAt`, serialized in the token JSON and defaulting sanely for old rows);
  `Transition.SetTimeoutAfter(d)` / YAML `after: 72h` mark a transition time-driven;
  and `Workflow.Due(now)` / `Workflow.NextDue()` expose deadlines as pure functions of
  the marking and an explicit `now` (testable with a fixed clock, no sleeping).
- Fleet timer scan (M4.4): new optional `workflow.DueStorage` and
  `workflow.TransactionalDueStorage` interfaces maintain a per-instance next-due index
  (`SaveVersionedStateWithDue`) and scan it with `ListDue(ctx, before, limit)`. The
  SQLite and Postgres backends implement them (new nullable `due_at` column plus an
  index) and are covered by the conformance suite; `store.EnsureSchema(ctx)` creates the
  column and index and idempotently migrates a pre-existing table. `Manager.ListDue`
  finds the overdue instances and `Manager.FireDue(ctx, id, def, now)` advances one —
  pinning the workflow clock to `now`, firing every due transition (skipping any whose
  guard rejects it), and saving under the same optimistic-concurrency retry loop as
  `Execute`. The Manager maintains the due index on every save path, so a host cron of
  ~10 lines implements "escalate if not approved in 3 days" across a persisted fleet,
  restart-safe by construction (state in the database, clock in the host).
- Timer docs + example (M4.5): new `docs/guides/TIMERS_GUIDE.md` explaining the
  host-driven time model (the no-internal-scheduler boundary, the API tour, the
  `ListDue`/`FireDue` fleet recipe, guard interaction, AND-join deadline semantics,
  tick-frequency guidance, and multi-host concurrency), plus a runnable
  `examples/timer_escalation` module: a `after: 72h` approval workflow, a fleet of
  SQLite-persisted instances at different ages, and a ~10-line cron tick advanced over
  a fixed clock so it runs instantly and deterministically.
- Replica-safe timer distribution example: new `examples/timer_escalation_beanstalkd`
  module showing the `ListDue`/`FireDue` fleet recipe across multiple worker replicas
  with Beanstalkd as a plain job distributor — a singleton dispatcher enqueues due
  instances, long-lived competing workers fire them, and the demo injects (and
  absorbs) both distributor failure modes live: a duplicate delivery no-ops via
  `FireDue`'s idempotency, and a crashed worker's job is redelivered to a peer.
  Ships a `docker-compose.yml` for the broker.
- Timer hardening (M4 review): several correctness fixes to the host-driven timer
  model before release:
  - `NewWorkflowFromMarking` now *adopts* a persisted marking: tokens that already
    carry an entry time keep it (a reloaded instance's running timers are restored
    rather than reset to construction time); only tokens without one are stamped.
  - `CreateToken`/`CreateTokens` now stamp entry times for timed definitions, so a
    place seeded directly (not via firing) starts its deadline correctly.
  - The transition timeout has a single source of truth: `SetTimeoutAfter` no longer
    mirrors the duration into `after` metadata (`TimeoutAfter()` is authoritative),
    and the Mermaid diagram derives the timer label from `TimeoutAfter()` directly.
  - `Manager.FireDue` no longer bumps the version on a pure no-op: a due-but-guard-
    blocked instance whose stored due index is already correct is left untouched,
    while a stale index (no live timer, or a future deadline) is saved so it
    self-heals.
  - `Manager.Execute` now rejects a `WithTxSideEffect` call for a timed definition on
    a backend that is a `DueStorage` but not a `TransactionalDueStorage` (which would
    silently corrupt the due index), mirroring the existing loud error for missing
    transactional support.
  - The SQLite due column clamps out-of-range instants (after ~year 2262) to
    `math.MaxInt64` instead of letting `UnixNano` wrap negative, so a far-future
    deadline can never look overdue.
  - Boolean-presence firing into an already-occupied place stays idempotent under
    timed definitions (no phantom duplicate token).

### Changed
- Minimum Go version is 1.25 (module and CI). It was raised to 1.24 during M0, then to
  1.25 in M1.3 because the pgx v5 PostgreSQL driver requires it.
- Postgres integration tests use a "both" strategy: `WORKFLOW_TEST_POSTGRES_DSN` when
  set (CI uses a service container), otherwise a throwaway Postgres via testcontainers
  when Docker is available; skipped when neither is present.
- Repository layout: planning/comparison/guide docs moved under `docs/`; not-yet-
  implemented CPN/HCPN schemas and examples quarantined under `docs/roadmap/`.

### Removed
- Stopped tracking the committed `examples/migration_example/migration_example.db`
  binary artifact.

### Breaking
- Persistence APIs now require a `context.Context`. Callers must pass one
  (e.g. `manager.SaveWorkflow(ctx, id, wf)`, `store.LoadState(ctx, id)`).
- The SQLite state table now includes a `version` column. Databases created by a
  previous version must be migrated before upgrading:
  `ALTER TABLE workflow_states ADD COLUMN version INTEGER NOT NULL DEFAULT 0;`
  (use your configured table name). New databases created via `GenerateSchema` include
  it automatically.
- The SQL state tables now include a nullable `due_at` column (the M4 timer index). The
  SQLite and Postgres backends advertise `DueStorage`, so `Manager` save paths write it;
  a table created by a previous version that lacks the column will fail on save until
  migrated. Call `store.EnsureSchema(ctx)` on startup — it adds the column and index
  idempotently on both fresh and pre-existing tables — or migrate by hand:
  `ALTER TABLE workflow_states ADD COLUMN due_at INTEGER;` (SQLite) /
  `ALTER TABLE workflow_states ADD COLUMN due_at TIMESTAMPTZ;` (Postgres), plus
  `CREATE INDEX workflow_states_due_at_idx ON workflow_states (due_at);`. Disable the
  index entirely with `storage.WithDueColumn("")` if you cannot migrate. New databases
  created via `GenerateSchema` include the column automatically.
