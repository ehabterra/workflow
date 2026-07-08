# Go Workflow — Production Roadmap

> Status of this document: **living plan**. Rewritten 2026-07-03 after a three-track
> audit (capability inventory, real-system readiness review, stale-doc review) of
> `main` @ 2f7597f (post-M2).
>
> Last updated: 2026-07-03

---

## How to read this

- **Milestones are ordered by what unblocks building a real system with this library**,
  not by feature breadth. Each is independently releasable and leaves the engine shippable.
- Every task has an **acceptance criterion** — the objective bar for "done".
- Effort for one experienced Go engineer: **S** = days, **M** = 1–2 weeks,
  **L** = 3–6 weeks, **XL** = 2–3 months.
- 🔴 blocks building a real system · 🟡 important for adoption · 🟢 polish / demand-driven.

Two standing principles:

1. **The code and the docs must never disagree.** Drift now exists in *both* directions
   (see "Current reality") and is treated as a defect, not a docs chore.
2. **Library, not engine.** This is an in-process library. Features that would turn it
   into a durable-execution server (internal schedulers, workflow-as-code replay,
   automatic retries) are explicitly out of scope — each gets an **honest boundary
   statement** in the docs instead of a half-implementation (see "Boundaries").

---

## Strategy (decided 2026-07-03)

**Positioning:** the *Symfony Workflow of Go* — the best **embedded, declarative,
Petri-net** workflow library for in-process use. Not a Temporal competitor.
Differentiators: true parallelism (AND-split/join), colored tokens, guards,
persistence + audit + diagrams, correctness under concurrency.

**Method:** stop adding breadth. Fix what blocks real use (M3), add the one genuinely
missing primitive (time, M4), then **dogfood** — build a near-real reference system
(M5) and let the friction log drive everything after. Old milestones M3–M8 (weighted
transitions, HCPN, compensation, enterprise, interop) are **parked as demand-driven**
until dogfooding proves they're needed. The full README rewrite stays deferred to the
very end — but *broken* README snippets are bugs and get fixed now (M3.9).

**Build vs. declare** (from the readiness review):

| Concern | Decision |
|---|---|
| Timers / timeouts | **Build** — host-driven tick, no internal scheduler (M4) |
| External signals / async waits | **Declare + thin API** — instance rests in a place; host fires the transition; add the load-fire-save helper (M3.5) |
| Crash recovery | **Declare + helper** — state (not code) is persisted; atomic apply+save+history in one tx (M3.5); side-effect idempotency documented as the host's job |
| Definition versioning | **Minimal build** — fingerprint stamp + check on load; full migration parked (M3.3) |
| Observability | **Build small** — OTel/metrics contrib on the existing event system (M5, during dogfood) |
| Testing utilities | **Build small** — `workflowtest` assertions + path runner (M5, during dogfood) |
| Docs / mental model | **Continuous** — deep pass after M4, grounded in the dogfood app |

---

## Current reality (validated 2026-07-03, three independent audits)

**Genuinely implemented and tested:**
- Boolean Petri-net core: definitions, guards (`expr-lang`), events/listeners (3-tier),
  AND-split/join parallelism, context. Root coverage **87.5%**.
- **Colored Petri Nets (M2, merged)**: unified `Marking` (boolean = uncolored token),
  `Token` value type, per-token firing (`ApplyTransitionForToken`), token-aware guards
  (`token.amount > 1000`), queries/aggregation, adaptive persisted format (old rows load).
- Storage: SQLite + Postgres with **optimistic concurrency** (built into `Storage`,
  `ErrConflict`), transactional building blocks (`RunInTx`, `*Tx` methods), and an
  exported **conformance kit** (`storagetest.Run`) — a real strength.
- History/audit (SQLite), YAML config with **strict decoding** + polymorphic
  `initial_marking`, Mermaid diagrams, Manager/Registry lifecycle.

**Absent (confirmed by grep, not vibes):** timers/scheduling, signals/correlation API,
definition versioning, observability hooks, user-facing test helpers, OR/XOR-joins,
weighted arcs, HCPN, static validation, compensation, REST API.

**Defects found by the readiness review (drive M3):**
1. 🔴 **Context persistence silently drops data** — only pre-declared custom-field
   columns round-trip; every other `SetContext` key is lost on save (`storage/config.go`).
2. 🔴 **Data races**: `Manager.SaveWorkflow` reads `wf.context` without the lock and
   hands the live map to storage (`manager.go:88`); whole-marking `Apply*` unlocks
   between enablement check and `moveMarking`, so two concurrent fires can both pass
   (`workflow.go:510-541`); `Definition`/`Manager` listener slices are unlocked.
3. 🔴 **Load validates only `places[0]`** against the definition; a stale marking with a
   removed place loads silently and the instance is stuck (`manager.go:61`,
   `workflow.go:673`).
4. 🔴 **Fire and save are uncoordinated** — `Apply*` mutates memory only; state, history,
   and side effects are separate steps with no atomic helper; `Manager` never writes
   history and can't join a transaction.
5. 🟡 `ErrTransitionNotAllowed` conflates "not enabled" with "guard rejected" — breaks
   webhook dedup and permission UX.
6. 🟡 Registry caches every instance forever (unbounded, stale in multi-replica).
7. 🟡 SQLite plain save uses `REPLACE INTO` (DELETE+INSERT: fires triggers, breaks FKs);
   history schema is SQLite-only (`AUTOINCREMENT`); storage `*Tx` methods ~0% covered.
8. 🟡 **Doc drift both directions**: README Quick Start/interfaces/YAML snippet don't
   compile or don't load (missing `ctx`, nonexistent `workflow.NewSQLiteStorage`, stale
   `initial_place` key rejected by strict decoding); `doc.go` and the old roadmap said
   CPN doesn't exist (it's merged); `docs/roadmap/cpn/*` (since deleted) + `banking_cpn.yaml` documented a
   YAML schema the loader **rejects**; "automatically logs every transition" is false
   (history is opt-in); "deadlock-free" is unbacked (no static validation).

---

## M0 — Truth & hardening — v0.4.0 — ✅ COMPLETE (2026-07-01)

Strict YAML decoding, example CI builds, lint, error model, `context.Context` on
interfaces, repo hygiene. (Details in git history.)

## M1 — Crash-safe storage (ACID) — v0.5.0 — ✅ COMPLETE (2026-07-02)

Transactional state+history building blocks, optimistic concurrency, Postgres backend,
`storagetest` conformance kit.

## M2 — Colored Petri Nets / Smart Tokens — v0.6.0 — ✅ COMPLETE (2026-07-02)

Unified marking (boolean = uncolored token), token model, per-token firing, token-aware
guards/events, queries/aggregation, polymorphic `initial_marking`, adaptive persisted
format, CPN guide + two runnable examples. Design notes in git history and
`docs/guides/CPN_GUIDE.md`.

---

## M3 — Real-system correctness (🔴) — target: **v0.7.0**

Fix everything the readiness review ranked as a blocker, **before** writing new features.
Small, high-leverage, mostly S-effort. This is the milestone that makes dogfooding honest.

Shipped in three PRs: **part 1** (#15) = M3.1–M3.2; **part 2** (#16) = M3.3, M3.4, M3.5a,
M3.6, M3.8; **part 3** = M3.5b, M3.7, M3.9 plus fixes for three defects an adversarial
review confirmed in part 2 (fingerprint separator collision; migration handler skipped
for removed-place markings + no reload after migration; Execute retry exhaustion under
contention — now backoff + jitter + WithMaxRetries; also: cache hits get the same
definition check as loads).

| # | Task | Status | Acceptance |
|---|------|--------|-----------|
| M3.1 | **Full context persistence** — always-on `context` JSON column (TEXT/JSONB); custom-field columns remain as queryable projections of it. | ✅ | `SetContext("k", v)` on any key round-trips through save/load on both backends; conformance kit covers it. |
| M3.2 | **Concurrency fixes** — copy `wf.context` under lock in `SaveWorkflow`; close the check-then-move gap in whole-marking `Apply*` (re-verify enablement under the write lock before `moveMarking`, mirroring `ApplyTransitionForToken`); add locking to `Definition`/`Manager` listener slices. | ✅ | New `-race` tests: concurrent same-transition fires never double-move; concurrent `SetContext` + `SaveWorkflow` is race-clean. |
| M3.3 | **Definition fingerprint** — `Definition.Fingerprint()` (SHA-256 over canonical places+transitions+guards); stored per instance; `LoadWorkflow` returns `ErrDefinitionMismatch` unless a migration hook is supplied. Also: validate **every** loaded place, not just `places[0]` (bug fix, independent of fingerprint). | ✅ | Editing a definition then loading an old instance fails loudly with both fingerprints; marking with an undefined place never loads silently. |
| M3.4 | **Split error sentinels** — `ErrNotEnabled` (marking) vs `ErrGuardRejected` (constraint), both wrapped by `ErrTransitionNotAllowed` so `errors.Is` on the old sentinel still works. | ✅ | Webhook redelivery test distinguishes "already fired" from "forbidden". |
| M3.5a | **Optimistic execute helper** — `Manager.Execute(ctx, id, def, fn)`: load fresh → fn → versioned save with bounded `ErrConflict` retry. | ✅ | Concurrent Executes lose no updates; injected-conflict test retries then succeeds. |
| M3.5b | **Atomic state+history tx** — fire + versioned state + history record in **one** `RunInTx` transaction (needs a storage-level helper; core `Manager` can't import `history`). Document listener side-effect semantics (at-least-once; idempotency is the host's job; outbox recipe). | ✅ | Kill-test: crash between fire and save never leaves state/history disagreeing. |
| M3.6 | **Registry hygiene** — opt-out of caching (`WithoutRegistryCache` / per-call fresh load) + `EvictWorkflow` after save for multi-replica; document staleness semantics. | ✅ | Multi-replica scenario test: replica B sees replica A's save (fresh-load mode). |
| M3.7 | **Storage hardening** — replace `REPLACE INTO` with proper upsert; make history schema portable (Postgres history backend or dialect-aware schema); unit-test the `*Tx` methods (currently ~0%). | ✅ | Conformance kit + history tests pass on both engines; FK to a state row survives a save. |
| M3.8 | **Instance listing** — `ListIDs(ctx, opts)` on storage (paged, optional `ListableStorage`), the missing primitive for "scan the fleet" (and for M4 `ListDue`). | ✅ | Host can enumerate persisted instances without raw SQL. |
| M3.9 | **Doc-drift hotfixes** (not the full README rewrite) — make every README snippet compile/load (`ctx` args, `storage.NewSQLiteStorage(db)`, `initial_marking`); fix `doc.go` status and `yaml/config.go:158` stale comment; correct "automatically logs" → opt-in helpers; drop/qualify "deadlock-free"; delete or rewrite `docs/roadmap/cpn/*` planning docs, `cpn_example_minimal.yaml`, `cpn_schema.json`, and `banking_cpn.yaml` (they document a schema the strict loader rejects); refresh the two comparison docs' "our side" columns (CPN now ships). | ✅ | A new user can copy-paste every README snippet successfully; no shipped file implies a feature the loader rejects. |

**Release gate:** the readiness review's top-5 blocker list is empty; `-race` suite green.

---

## M4 — Time, host-driven (🔴 for real systems) — target: **v0.8.0**

The single most-demanded real-world primitive ("escalate if not approved in 3 days").
**Design decision:** the library *models* time; the **host owns the clock** (cron/ticker/
queue). No goroutines, no internal scheduler — keeps the core deterministic and testable.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M4.1 | **Entered-at stamping** — marking records when each place received its tokens (`produce`); serialized in the token-object JSON form; old rows default sanely. | S | Round-trips both backends; old rows still load. |
| M4.2 | **Timeout on transitions** — `Transition.SetTimeoutAfter(d)`; YAML `after: 72h`. | S | Strict loader accepts `after`; metadata visible in diagrams. |
| M4.3 | **Due API** — `Workflow.Due(now) []Transition`, `Workflow.NextDue() (time.Time, bool)`. `now` is always a parameter (testable with a fake clock, no wall-clock in core). | S | "Wait 30 min then fire" / "timeout after 24h" expressible and unit-testable without sleeping. |
| M4.4 | **Fleet scan** — `ListDue(ctx, before, limit)` on storage (maintained due-index column on save) + `Manager.FireDue(ctx, id, def, now)`. | M | Host cron of ~10 lines implements 3-day escalation; restart-safe by construction (state in DB, clock in host). |
| M4.5 | **Timer docs + example** — escalation recipe; explicit boundary statement (no internal scheduler; here's the cron/queue pattern). | S | Example runs; guide explains the host-driven model. |

---

## M5 — Dogfood: reference system + the tools it demands (🟡) — target: **v0.9.0**

Build a near-real **expense-approval service** (see `docs/DOGFOOD.md` when started):
parallel legal+finance approvals (AND-split/join), 3-day deadline escalation (M4),
webhook-resumed waits, fleet of SQL-persisted instances, audit trail, metrics — plus one
CPN-flavored component (batch payment run over approved-expense tokens) so both the
instance-per-entity and tokens-in-one-net models get exercised.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M5.1 | **The reference system** — separate module (`examples/expense_approval` or own repo), HTTP layer, Postgres, cron tick, OTel. | ✅ | Runs end-to-end; kill-tests pass; README of the example doubles as the "mental model" tutorial. Ships with listener-based metrics; the OTel contrib is M5.3, extracted from it. Spec: `docs/DOGFOOD.md`. |
| M5.2 | **`workflowtest` package** — marking assertions, path runner ("apply submit→legal_approve→finance_approve, assert approved"), guard-env harness, fake-clock helpers for `Due`. Built *as the dogfood needs them*. | M | The reference system's tests use only public helpers. |
| M5.3 | **Observability contrib** — `contrib/otel`: Manager-listener span pair + `firings_total{workflow,transition,result}`; non-erroring **observer listeners** (instrumentation must never block business flow); lifecycle + rejection events as needed. | M | Traces visible in a collector from the reference system. |
| M5.4 | **Friction log → issues** — every papercut found while building becomes a tracked issue; this list *is* the input to re-prioritizing the parked milestones. | 🔄 | Written verdict: what the library made easy, hard, impossible. Running log: `docs/roadmap/FRICTION.md` (7 entries from M5.1); graduates to issues at M5 exit. |

**Exit criterion / go-no-go:** the reference system is built **without** bespoke
persistence or scheduling layers papering over the library. If it still needs them,
that finding outranks any roadmap item.

---

## M6 — Docs & mental model (🟡) — target: **v0.10.0**

The deep pass, grounded in the dogfood app (not before): a narrative "how to think in
Petri nets" guide, the signals/crash-recovery/idempotency recipes, `Boundaries` doc
(below), pkg.go.dev polish. The **full README rewrite still lands last**, at the v1.0 gate,
describing only shipping behavior.

---

## Post-M5 — friction-log-driven features

- **Cancellation regions (reset arcs)** — ✅ shipped (2026-07-07), the friction
  log's #1 finding. `Transition.SetResets` / YAML `resets:` clear declared
  places atomically on firing (validated, fingerprinted, timer-killing,
  Mermaid-rendered); the dogfood's reject transitions adopted it and deleted
  their token surgery.

- **OR-input transitions (`from_any`)** — ✅ shipped (2026-07-07), friction #2.
  Enabled by any one marked input, consuming only it; fingerprinted, timer-
  aware, per-token capable, Mermaid-labeled "(or)". Dogfood collapsed its
  four `*_escalated` twin transitions.
- **XOR-split routing (`Workflow.ApplyAny`)** — ✅ shipped (2026-07-07),
  friction #3. Fires the first allowed candidate; guard-routed alternatives
  become one call.

## Parked — demand-driven (🟢, revisit after M5 friction log)

Formerly M3–M8. None is deleted; none proceeds without a dogfood-proven need.

- **Weighted transitions & advanced sync** (N-token thresholds, OR/XOR-join,
  discriminator, cancellation regions). Note: per-token AND-join is impossible today
  (`ApplyTransitionForToken` is single-input) — promote this specific gap if the dogfood
  batch component hits it.
- **Static validation / `workflow lint`** (reachability, deadlock detection) — also the
  precondition for ever claiming "deadlock-free" in marketing.
- **HCPN / nested workflows**; **compensation/saga**; **message correlation** beyond
  id-keyed lookup.
- **Enterprise**: full definition migration (beyond the M3.3 fingerprint), RBAC,
  templates, analytics.
- **Interop**: YAML export (stream with an encoder — don't buffer large documents in
  memory), PNML, REST API, packaged web UI.

---

## Boundaries (honest statements, to be written into docs in M6)

1. **Not a durable-execution engine.** State is persisted, code is not. A crash loses
   in-memory progress since the last save — by design. Recovery = load + re-drive.
2. **No internal clock or scheduler.** Time enters via the host (M4 tick model).
3. **Listeners run at-least-once relative to persisted state.** Side effects need
   idempotency keys or an outbox; the library provides the transaction hook, not the
   guarantee.
4. **History is opt-in**, wired through the atomic helper (M3.5) — never silently
   automatic.
5. **Org structure / assignment stays in the host app**
   (see `docs/guides/ORGANIZATIONAL_FEATURES_EXPLAINED.md`).

---

## v1.0.0 gate

Every advertised feature implemented, tested (≥90%), documented; two storage backends
(state **and** history); M3 defect list empty; timers + observability + test utilities
shipped; the reference system running; README rewritten to describe only shipping
behavior; "not production ready" banner removed.

## Cross-cutting definition of done (every task)

- Unit + integration tests, ≥90% package coverage, `-race` clean.
- Godoc on all new exported symbols; user-facing docs updated in the same PR.
- No feature merged whose docs/examples can't actually be executed by the engine.
- CHANGELOG entry; benchmark for any hot path.
