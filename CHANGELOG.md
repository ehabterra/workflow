# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- **Dynamic-cardinality joins** (issue #34) — a transition can now be enabled by
  a token **count resolved at fire time**, not fixed by the arc structure. The
  classic AND-join asks only "is this place marked?", so the number of things a
  transition waits for is a property of the drawing; the recurring business
  shape — an approval chain whose length comes from the record's value — is not.
  `Transition.SetRequirements` (YAML `require:`) declares, per input place, a
  `count` expression, an optional `where` predicate selecting which tokens
  count, and an optional `distinct` field so two tokens carrying the same value
  count once. Both expressions are compiled at definition-build time (a
  malformed one fails the load, not the first firing of a rare branch) and are
  evaluated against the workflow context plus the place's tokens.
  A requirement is both an enablement condition **and a token selector**: firing
  consumes exactly the tokens it selected and **leaves the remainder in the
  place**, where an ordinary input place is drained. Selection is deterministic
  (the place's own order) and is re-resolved under the write lock, so concurrent
  firings split a pool instead of double-consuming it. An unmet requirement is
  `ErrNotEnabled` — so an `ApplyAny` candidate simply loses to its sibling —
  while a requirement that cannot be *evaluated* is a loud error rather than a
  silent skip.
  Two combinations are rejected at `NewDefinition`, because each would put two
  competing token selectors on one transition: `require` with `from_any`, and
  two requirements on the same place. A requirement's place must be one of the
  transition's own inputs. Per-token firing (`ApplyTransitionForToken`) is
  likewise rejected on a transition with requirements — the requirement *is* the
  selection. Reset arcs are unchanged and still clear whole places.
  Requirements are part of `Definition.Fingerprint()` and appear in
  `Diagram()` on the transition node (the arity is an expression, not a number
  of arcs, so a diagram that omitted it would misstate when the transition can
  fire). **Fingerprint compatibility:** like the effects segment, the
  requirements segment is written only when a transition declares one, so
  definitions written before this feature fingerprint exactly as they did.
  Re-measured on `internal/spike/approval`: declarative coverage **50% → 68% by
  concern**, **18% → 26% by line**, and this time **Go shrank by 73 lines** —
  the ledger read, the chain-satisfaction test (including the parameter that
  forced the host to simulate the write it was about to make), the
  chain-membership check, and the effect that appended to the ledger table are
  all deleted, not relocated.
- **Declarative transition effects** (issue #36) — a transition can now declare
  what happens when it fires, not just that it may. `Transition.SetEffects`
  (YAML `effects:`) binds named, ordered effects that run **inside the
  state-save transaction**; `SetAfterCommit` (YAML `after_commit:`) declares the
  phase that runs only once the transaction has committed, for work that must
  not be transactional. Implementations are registered once against an
  **`EffectRegistry`** (`Register` / `RegisterAfterCommit`, plus `Validate` to
  catch an unimplemented name at startup rather than the first time a rare
  branch fires) and wired in with `workflow.WithEffectRegistry`.
  Effects receive an **`EffectEvent`** — instance id, transition, the marking
  either side of the firing, declared params, and a copy of the instance context.
  Because effects bind to the transition, two guarded transitions out of one
  place fire *different* effect sets, so an `ApplyAny` branch no longer needs a
  host-side switch. A definition that declares effects without a registry is a
  loud error, never a silent skip. **After-commit effects are at-least-once** —
  documented on `AfterCommitFunc`; the library provides the phase, not the
  guarantee.
  **Fingerprint compatibility:** the effects segment is appended to a
  transition's structural record only when it declares effects, so a definition
  written before this feature fingerprints exactly as it did and instances
  persisted by earlier versions keep loading without a migration. Effect order
  and params ARE fingerprinted (order is execution order, so two orderings are
  two different definitions).
  Measured on `internal/spike/approval`: declarative coverage **30% → 50% by
  concern**, **10% → 22% by line**; the per-branch effect switch and the
  post-commit collection are gone. Total Go barely moved — the win is that the
  sequencing decisions left Go, not that lines vanished.
- **`internal/spike/approval` — declarative-coverage measurement** (issue #45). A
  realistic value-escalated approval workflow (threshold ladder, approvals
  ledger, separation of duties, admin last-resort, revision supersession,
  audit/notify/outbox in-tx plus a post-commit email) implemented against only
  what the library ships today, so the value proposition can be measured rather
  than estimated. Result: **10% of the workflow logic is declarative by line,
  30% by concern** — the library covers the status graph, while the dynamic
  approval join, guard inputs, effect binding, projection, and multi-instance
  cascade all stay in Go. Its `COVERAGE.md` carries the measurement and a
  friction log ranked by how much hand-written Go each gap forces.
  `TestSupersedeCascade_DivergesMarking` documents a real defect the current
  workaround causes: a superseded record's status and its workflow marking
  disagree permanently, because there is no atomic multi-instance transition.
- **`CLAUDE.md`** — guidance for AI agents: module layout, the `-p 1`
  test-serialization rule (Postgres testcontainers), coverage reality, PR/commit
  conventions, the architecture invariants, and the non-obvious cautions
  (non-reentrant `RWMutex`, Mermaid `#nnn;` entity escaping, instance-only START
  marker, `contrib/otel` MVS pins, build/db artifacts).
- **MIT copyright headers** on every `.go` file, stamped with each file's git
  creation year.
- **Test coverage raised** with real (behavior-asserting) tests: root 95.6%,
  `history` 96.7%, `yaml` 95.0%, `workflowtest` 95.8%; `storage` 89.3% and
  `contrib/otel` 89.6% (residual is DB-fault / timing edge branches).
- **README rewritten** (early v1.0-gate item; the gate itself stays open —
  still pre-1.0). Shipping behavior only: honest positioning and an
  "Is this the right tool?" section (including when NOT to use it), a quick
  start extracted from the README and verified to compile and run verbatim,
  the model in sixty seconds with the renderer's own diagram, a feature
  tour where every claim links to its guide, the persistence contract with
  a bring-your-own-driver section (custom backends hand transactional side
  effects their own tx type — e.g. `pgx.Tx` — validated by `storagetest`),
  and docs/examples tables. Go version badge corrected to 1.25+.
- **`workflowtest` package** (M5.2) — public test helpers, the counterpart
  to the `storagetest` conformance kit: `AssertMarking` (exact set) /
  `AssertHas` / `AssertNotHas` marking assertions; the `Apply` path runner
  ("apply submit → legal_ok → finance_ok → finalize", failing with the step
  and marking on the first error); the **`AssertGuard`** table harness,
  which evaluates a transition's guards against context and token cases on
  a throwaway instance — no storage, no Manager (`AssertGuardAllows` /
  `AssertGuardRejects` shorthands); and **`Clock`** (frozen, `Advance`/`Set`)
  plus `AssertDue` so timer tests never sleep. The dogfood adopted the
  guard harness for its submit boundary and the marking assertions in the
  payment-migration test.
- **Observer listeners + OpenTelemetry contrib** (M5.3). `AddObserver` on
  `Definition`, `Manager`, and `Workflow` registers a **non-blocking**
  listener (`ObserverFunc`): it returns nothing, its panics are recovered,
  and it can never abort or fail a firing — the contract instrumentation
  needs. Guard rejections became observable through the new
  **`EventGuardRejected`**, an observability-only event emitted when an
  expression constraint or blocking guard listener refuses a firing
  (whole-marking and per-token paths); it is dispatched exclusively to
  observers, so error-returning listeners can never add a failure mode to
  the rejection path. On top of this sits the new **`contrib/otel`** module
  (separate go.mod — the core stays dependency-free):
  `otelworkflow.Instrument(mgr)` emits a `workflow.fire` span per completed
  firing — parented on the context the transition was applied with, its
  start time taken from the before-transition event — and the
  `workflow.firings` counter with `workflow.name`/`workflow.transition`/
  `workflow.result` (`applied` | `guard_rejected`) attributes. The dogfood
  exports both over OTLP/HTTP via the new `-otel-endpoint` flag.
- **M6 documentation pass** — the deep docs milestone, grounded in the
  dogfood app: `docs/guides/MENTAL_MODEL.md` (the narrative "how to think
  in Petri nets" guide, from marking-vs-status-column through a worked
  requirements-to-net translation), `docs/guides/PRODUCTION_RECIPES.md`
  (crash windows and idempotency — friction entries 5–8 as recipes:
  `Execute` retry resets, exactly-once audit, webhook redelivery mapping,
  cross-instance saga + reconciler, creation-seed GC, listener semantics,
  token-query re-entrancy, migration-as-policy), and `docs/BOUNDARIES.md`
  (what the library deliberately does not do). Godoc polished to match:
  refreshed package doc, a RETRY RESETS callout with example on
  `Manager.Execute`, and a LOCK RE-ENTRANCY warning on the token-query
  APIs. The README gained a documentation index.
- **Structural diffs for definition migration** (friction log #5). Every
  save stamps a compact definition *shape* (sorted place names plus a short
  hash of each transition's structural record, a few hundred bytes under
  the reserved `__workflow_def_shape` context key) alongside the
  fingerprint. On a mismatch, the `WithDefinitionMigration` handler now
  receives a **`DefinitionMismatch`** carrying both fingerprints and a
  **`DefinitionDiff`** — places and transitions added/removed/changed, by
  name (a renamed place reads as remove+add with its referencing
  transitions marked changed). `Diff.Additive()` turns "new structure only,
  approve mechanically" into a one-line policy; `Diff` is nil for state
  saved by pre-shape versions ("no information", not "no change"). The
  dogfood's hook now refuses non-additive expense-net changes instead of
  approving every upgrade blindly.
- **`WithFireDueTxSideEffect` — exactly-once timer audit records** (friction
  log #4). A `FireDue`-scoped transactional side effect that receives the
  steps the pass fired (**`FiredStep`**: transition + the marking before and
  after it) *inside* the save's transaction. A plain `WithTxSideEffect`
  could never serve here — `FireDue` returns the fired names only after its
  save commits, leaving an at-least-once crash window for post-hoc history
  writes. Now a timer firing and its audit record commit or roll back
  together, exactly like an interactive `Execute` fire; the effect is
  skipped when the pass fired nothing, and plain `Execute` rejects the
  option (only `FireDue` knows what fired). The dogfood's escalation tick
  deleted its post-hoc write and its timer records gained from/to markings.
- **Normalized token storage + cross-instance token queries.** The SQL
  backends now persist a marking as one row per token in a child table
  (`<state_table>_tokens`: workflow_id, place, token_id, full token JSON)
  instead of a single JSON blob in the state column — same whole-marking
  overwrite, same per-instance optimistic version, so the atomic
  load→fire→save contract is unchanged, but the marking becomes queryable:
  the new **`workflow.TokenQueryStorage`** optional capability (exposed as
  **`Manager.ListPlaceTokens`**) answers "every token in place P across ALL
  instances" — the shared-pool read-model — with one indexed query. Loads
  stay single-snapshot (the token rows LEFT JOIN into the instance-row
  query); saves write instance row + token rows in one transaction.
  Compatibility is self-healing: legacy rows (marking JSON still in the
  state column) load as-is and normalize on their next save, or eagerly via
  the new `BackfillTokenStates` helper (the dogfood runs it at boot);
  `storage.WithTokenTable("")` opts out entirely and restores the old blob
  format. `EnsureSchema` creates the table; `GenerateTokenSchema` emits the
  DDL for migration tools (see `examples/migration_example`'s new 000005
  migration). Conformance: the kit gained `Tokens/ListPlaceTokens`. Design:
  `docs/roadmap/SHARED_POOL_MODELING.md` §9 ("B via storage-only", simple
  flavor; per-token delta concurrency deliberately deferred).
- **Empty markings persist** — friction log #3. A marking with zero marked
  places is now a valid state end-to-end: `NewWorkflowFromMarking` accepts an
  empty (non-nil) marking, the new **`Manager.CreateWorkflowFromMarking`**
  creates and saves an instance from any marking (multi-place, colored
  tokens, or empty), loading no longer rejects a stored state with no places,
  and YAML may omit `initial_marking` entirely. This is what a pure
  token-pool net needs — all its places are legitimately empty between
  batches — so the dogfood deleted its always-marked `batch_control` anchor
  place and instead shows real place-removal migration in its
  `WithDefinitionMigration` hook (strip the anchor from stored markings
  before the manager reloads). The storagetest conformance kit gained
  `EmptyMarkingRoundTrip`. Design context in
  `docs/roadmap/SHARED_POOL_MODELING.md`.
- **OR-input (merge) transitions** — friction log #2. `from_any: true` in
  YAML (`Transition.SetFromAny` in Go) makes a transition enabled when ANY
  ONE of its input places is marked; firing consumes exactly the first
  marked input, leaving the others untouched. One transition now serves
  "the same action from either stage" (approve from pending OR escalated),
  collapsing twin transitions. Works in every apply path including
  per-token firing (the input is resolved to whichever place holds the
  token), timers (due when any marked input has waited long enough), the
  fingerprint (input mode is part of the structure), and Mermaid diagrams
  (per-input edges labeled "(or)" instead of a join node).
- **`Workflow.ApplyAny(ctx, names...)`** — friction log #3, the XOR-split
  resolver. Fires the first named transition the state allows, skipping
  not-enabled and guard-rejected candidates, returning the fired name; when
  nothing fires, the last blocking error tells the caller whether nothing
  was enabled or a guard refused. Guard-routed alternatives out of one
  place (auto-approve vs. review) become one call.
- **Rich Mermaid renderer, in the library**: `Workflow.Diagram()` was
  rewritten as a flowchart designed for technical and non-technical readers,
  and `Definition.Diagram()` (structure-only) is new. Places are stadium
  nodes with the classic ◉ entry marker and a distinct terminal style;
  transitions are rectangles color-typed by nature — ⏱ timed transitions
  (derived from `TimeoutAfter`, amber with dashed edges) and a documented
  `diagram_class` metadata key ("person", "auto", or any host classDef) for
  actor typing the engine cannot know. Guards appear visibly on the routing
  edges, prettified (`❰ amount ≤ 100 ❱`); reset arcs are dotted red
  "cancels" edges. Splits and merges route through gateway diamonds with
  the BPMN symbols: a multi-input transition joins through ◇+ (parallel
  gateway, AND-join) or ◇× (exclusive gateway, OR-input: exactly one
  consumed), and a multi-output transition forks through ◇+ (outputs
  always all fire); XOR-splits stay as guard-labeled alternative edges
  out of the choosing place.
  Places may name a region via a `diagram_group` metadata key; same-group
  places are boxed in a Mermaid `subgraph` so parallel lanes read as regions
  (the dogfood boxes the expense net's Legal/Finance review lanes). On a live
  instance the current marking is highlighted and colored-token places carry
  ⬤×N badges. Both `Diagram` methods take an optional **`DiagramDirection`**
  (`DiagramDirectionTopDown` — the default — `BottomUp`, `LeftRight`,
  `RightLeft`; unknown values fall back to the default), and the default
  orientation changed from left-right to **top-down**, which reads better on
  scrolling pages. Every example UI gained a flow-direction switcher
  (`?dir=` on the dogfood's diagrams/expense/batch pages, advanced_workflow,
  and website_workflow), and `advanced_workflow` was aligned with the
  current feature set: its seven per-stage `reject_*` twins collapsed into
  one OR-input `reject_project` whose reset arcs cancel sibling reviews,
  its team metadata became `diagram_group` lanes, human decisions carry
  `diagram_class: person`, the drifted role→transition lists were fixed,
  and both stale old-renderer diagram templates (tooltip JS for output
  that no longer exists) were rewritten for the current renderer. The old stateDiagram output (and its HTML tooltip spans) is
  gone. The dogfood embeds these diagrams on `/diagrams`, every expense page,
  and the batch page, with an HTML legend.
- **Per-place metadata on `Definition`** (`SetPlaceMetadata` /
  `PlaceMetadata`): places are bare strings and cannot carry their own
  metadata like transitions, so a Definition-level side-table now holds it.
  It is cosmetic (currently diagram hints) and excluded from `Fingerprint`,
  so it never invalidates persisted instances. The YAML loader now attaches
  place `metadata:` to the Definition instead of dropping it in
  `LoadDefinition`.
- **Cancellation regions via reset arcs** — the top-ranked finding of the M5
  friction log. A transition can declare places it CLEARS when it fires:
  `Transition.SetResets("b", "c")` in Go, `resets: [b, c]` in YAML. Firing
  consumes the inputs, empties every reset place, and only then produces the
  outputs (a place that is both reset and an output keeps what the firing
  produces) — all inside the same atomic critical section as the marking
  move, in every apply path including per-token firing. Reset places do not
  affect enablement. Any timer running on a cleared token dies with it, so a
  rejected instance can no longer leave a zombie escalation in the due
  index. Reset places are validated by `NewDefinition`, included in the
  definition fingerprint, and rendered in Mermaid diagrams. The canonical
  use: a reject transition on one review branch resets the sibling branch,
  cancelling its pending work — previously host-side token surgery.

### Breaking
- **`DefinitionMigrationFunc` signature changed**: the handler now receives
  one `DefinitionMismatch` struct (workflow ID, stored/current fingerprints,
  structural `Diff`) instead of `(id, storedFingerprint, currentFingerprint)`
  strings — update handlers to
  `func(ctx context.Context, mm workflow.DefinitionMismatch) error`. The
  fingerprint itself is unchanged; the newly stamped shape rides in a second
  reserved context key (`__workflow_def_shape`), stripped on load like the
  fingerprint.
- The definition fingerprint encoding now includes each transition's reset
  places and input mode (AND-join vs OR-input), so **every fingerprint
  changes** with this version. Persisted
  instances load again after a `WithDefinitionMigration` hook approves the
  upgrade (the loader still validates every place).
- **Versioning is now part of the `Storage` contract** (the library is pre-1.0
  with no compatibility guarantees; an unversioned backend loses updates by
  design, so the contract no longer allows one). `LoadState` returns
  `(marking, context, version, error)` in one atomic snapshot; `SaveState`
  takes `expectedVersion` (0 creates) and returns the new version. The
  `VersionedStorage` interface and the `LoadVersionedState`/`SaveVersionedState*`
  method family are gone — the `Versioned` prefix is dropped everywhere
  (`SaveStateTx`, `SaveStateInTx`, `SaveStateWithDue`, `SaveStateInTxWithDue`).
- **Fresh loads are the Manager default.** The registry cache is now opt-in via
  `WithRegistryCache()` (single-process optimization); `WithoutRegistryCache()`
  is gone. Fresh loads are correct in every deployment shape and optimistic
  concurrency protects the saves.
- **A persisted instance without a definition fingerprint no longer loads
  silently.** Every save stamps one, so a missing fingerprint is treated as a
  mismatch: `ErrDefinitionMismatch`, or the `WithDefinitionMigration` handler
  with an empty `storedFingerprint`.
- Removed legacy surface: the `history.SQLiteHistory` alias (use `SQLHistory`
  via `NewSQLiteHistory`/`NewPostgresHistory`), `storage.Initialize` (use
  `EnsureSchema`), and yaml's by-target-places `ApplyTransitionWithHistory`
  (use `ApplyTransitionByNameWithHistory`). yaml history records now store the
  full comma-joined place sets in from/to instead of a lossy "primary" place,
  and template value lookup is string-keys-only (`WithTemplateValue`) — the
  typed-key fallback could never match and has been removed.

### Fixed
- **Lost update under concurrency**: `LoadVersionedState` on both SQL backends
  read the marking and the optimistic-concurrency version in two separate
  queries, so a commit landing between them paired a stale marking with the
  new version — a subsequent save from that snapshot passed the version check
  and silently overwrote the concurrent update. Found by the dogfood reference
  system's tick-vs-approve race test (a timer escalation resurrecting an
  already-approved branch). Both backends now read state, context, and version
  in one query; the conformance kit gained a `Versioned/LoadIsAtomicSnapshot`
  stress test that fails on the old implementation.

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
