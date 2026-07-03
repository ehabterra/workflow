# Go Workflow — Production Roadmap

> Status of this document: **living plan**. It tracks the work required to take this
> engine from "solid core + aspirational docs" to a **reliable, feature-complete,
> production-grade** library that delivers every capability advertised in the README.
>
> Last updated: 2026-07-01

---

## How to read this

- **Milestones (M0–M8)** are ordered by dependency and risk, not by marketing priority.
  Each milestone is intended to be independently releasable (a git tag) and leaves the
  engine in a shippable state.
- Every task has an **acceptance criterion** — the objective bar for "done".
- Effort is a rough order of magnitude for one experienced Go engineer: **S** = days,
  **M** = 1–2 weeks, **L** = 3–6 weeks, **XL** = 2–3 months.
- 🔴 blocks a production claim · 🟡 important for adoption · 🟢 polish / nice-to-have.

The single most important principle: **the code and the docs must never disagree.**
Today they do (see M0). Until they don't, no "production" claim is defensible.

---

## Current reality (validated 2026-07-01)

**What genuinely works and is tested (core coverage 91%):**
- Boolean-marking Petri-net engine: definitions, places, transitions, parallel/branching.
- Guard expressions via `expr-lang/expr` with `hasRole` / `hasPermission` / `in` helpers.
- Pluggable `Storage` + `History` interfaces with a working SQLite implementation.
- Thread-safe `Registry` and `Manager`; CI runs with `-race`.
- Mermaid diagram generation; event/constraint systems; YAML config loading.
- Web UI **example** (advanced_workflow); 7 examples total.

**What is advertised but does NOT exist in code (the gap this roadmap closes):**
- Colored Petri Nets / Smart Tokens (no `token.go`, no `Token` type).
- Hierarchical / nested workflows (HCPN).
- ACID / transactional persistence; compensation / rollback.
- Advanced synchronization (AND/OR/XOR merge, discriminator).
- Timed transitions (TPN/DPN), scheduling.
- Static workflow validation (deadlock/reachability checking).
- Message correlation between instances.
- Weighted transitions, versioning, templates, RBAC, analytics, PNML export, REST API.

**Active correctness bug:** the YAML loader uses non-strict decoding, so CPN keys
(`cpn_enabled`, `token_schemas`, `token_selection`, …) in `yaml/cpn_example_minimal.yaml`
and `examples/banking_system/banking_cpn.yaml` are **silently dropped** — the file loads
as a plain boolean workflow and ignores all token semantics. This is a silent-wrong-behavior
defect and the top priority.

---

## M0 — Truth & hardening (🔴 no new features) — target: **v0.4.0** — ✅ **COMPLETE (2026-07-01)**

Make the repo honest and safe *before* building anything new. Small, high-leverage.
All tasks below are done: full suite passes with `-race`, golangci-lint is clean, and
every example module builds in CI.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M0.1 | **Strict YAML decoding** — use `yaml.Decoder.KnownFields(true)`; unknown keys error with file+line. | S | Loading `cpn_example_minimal.yaml` returns a clear "unsupported field" error instead of silently succeeding. |
| M0.2 | **Quarantine unimplemented docs/examples** — move CPN/HCPN schema + examples under `docs/roadmap/` or clearly stamp them `PLANNED — not yet implemented`. | S | No shipped `.yaml`/`.json`/README implies a feature the engine can run. |
| M0.3 | **Untrack binary artifact** — `git rm --cached examples/migration_example/migration_example.db`; verify `*.db` ignore covers subdirs. | S | `git ls-files | grep '\.db$'` is empty. |
| M0.4 | **golangci-lint** — add `.golangci.yml` (staticcheck, errcheck, ineffassign, gosec, revive) + CI job. | S | CI fails on new lint violations; baseline is clean. |
| M0.5 | **Build & smoke-test every example in CI** — examples with own `go.mod` are never compiled today. | S | CI matrix builds/vets each `examples/*` module. |
| M0.6 | **Error model** — wrap with `%w`, expand sentinels, ensure `errors.Is/As` works across `storage`/`yaml`/core. | M | Callers can branch on `ErrTransitionNotAllowed`, storage-not-found, guard-failure, etc. |
| M0.7 | **`context.Context` on `Storage`/`History` interfaces** — decide now; it's a breaking change best done pre-1.0. | M | All persistence methods accept `ctx`; SQLite impl honors cancellation. |
| M0.8 | **Package `doc.go` + godoc audit** — every exported symbol documented. | S | `go doc ./...` reads cleanly; pkg.go.dev score improves. |
| M0.9 | **Repo hygiene** — move planning docs to `docs/`; add `CONTRIBUTING`, `CHANGELOG`, `SECURITY`, `CODE_OF_CONDUCT`, issue/PR templates. | S | Root has README + LICENSE + ROADMAP + CHANGELOG only. |

**Release gate:** everything advertised as "Current ✅" is true, tested, and lint-clean.

---

## M1 — Crash-safe storage (ACID) (🔴) — target: **v0.5.0** — ✅ **COMPLETE (2026-07-02)**

Durability is the foundation every later feature leans on. Do it before tokens.
All tasks done: transactional state+history saves, optimistic concurrency, a second
(PostgreSQL) backend, and a shared conformance kit that both backends pass.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M1.1 | **Transactional save** — state save + history append commit atomically (single tx). | M | Kill-test: crash injected mid-transition never leaves state and audit log disagreeing. |
| M1.2 | **Optimistic concurrency / versioning** — per-instance version column; reject stale writes. | M | Two concurrent `SaveWorkflow` on same id: one succeeds, one gets `ErrConflict`. |
| M1.3 | **Postgres backend** — second reference impl proving the interface isn't SQLite-shaped. | L | Same conformance test suite passes on SQLite and Postgres. |
| M1.4 | **Storage conformance test kit** — exported test harness any backend can run. | M | `storagetest.Run(t, factory)` validates any `Storage`. |

---

## M2 — Colored Petri Nets / Smart Tokens (🔴, headline feature) — target: **v0.6.0** — ✅ **COMPLETE (2026-07-02)**

**Design decision (2026-07-02):** rather than a separate "CPN mode" bolted onto boolean markings, the model was **unified** — a boolean/elementary net is the trivial case of a CPN (places hold uncolored tokens). One `Marking`, one `Workflow`; the token methods are always present and cost is pay-for-what-you-use. This eliminated the dual-constructor / optional-`CPNMarking` / `ErrNotCPN` machinery. Since the project is pre-1.0, backward compatibility of the API was intentionally not preserved, but the *persisted wire format* is backward compatible (adaptive: old place arrays still load, no data migration).

| # | Task | Status | Notes |
|---|------|--------|-------|
| M2.1 | **Token model** (`token.go`): `Token`, `TokenID`, `TokenData`, equality, validation. | ✅ | Value type, copy-on-access data, adaptive JSON. |
| M2.2 | **Unified marking** — one `Marking` with presence + token views (`TokensAt`/`AddToken`/`RemoveToken`/`TokenCount`/`AllTokens`); token ops on `Workflow`. | ✅ | Boolean = uncolored tokens; no opt-in mode. All boolean tests pass. |
| M2.3 | **Token-aware transitions** — `moveMarking` preserves colored tokens through firing; `ApplyTransitionForToken`; `SelectTokens`. | ✅ | Fixed the whole-marking-reset bug that wiped tokens at unrelated places. |
| M2.4 | **Token persistence** — full marking persisted (SQLite + Postgres); adaptive format. | ✅ | Old place-array rows still load (no migration). Conformance kit has a colored-token round-trip. Pulled forward into the M2.2 commit. |
| M2.5 | **CPN in YAML** — polymorphic `initial_marking` (scalar / list / map) declares the starting marking incl. colored tokens; `ClearPlace`. | ✅ | Replaced `initial_place` + `initial_tokens` with one polymorphic `initial_marking` key, mirroring the unified marking model (scalar = presence shorthand, map = colored tokens). Retired the aspirational `cpn_enabled`/`token_schemas` stubs; strict decoding still rejects unknown keys. |
| M2.6 | **Token transformation & queries** — `FindTokens`, `CountTokens`, `AggregateTokens` (count/sum/min/max/avg), `TransformTokens`. | ✅ | Go-func predicates/transforms (flexible, testable). |
| M2.7 | **CPN docs + example** — `docs/guides/CPN_GUIDE.md`, `examples/cpn_batch_processing/` (runnable). | ✅ | Example runs end-to-end; guide covers model, API, YAML, persistence, migration. |

**Token-aware guards & events (added 2026-07-03):** `Event`/`GuardEvent` now carry the tokens involved in a firing (`Event.Tokens()`), and the guard expression environment exposes `token`/`tokens`. This makes **attribute-routing declarative** — a transition guard like `token.amount <= 1000` gates per-token firing (the README `route_by_amount` shape). `NewEvent`/`NewGuardEvent` gained a `tokens []Token` parameter (breaking, pre-1.0). Also hardened the constructors: `newWorkflow` derives `initialPlaces` from the marking (single source of truth, no drift).

---

## M3 — Weighted transitions & advanced synchronization (🟡) — target: **v0.7.0**

Builds directly on the token model.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M3.1 | **Weighted transitions** — require N tokens in a place to fire. | M | "Need 3 approvals" transition fires only at 3 tokens. |
| M3.2 | **Advanced merge patterns** — AND-join, OR-join, XOR, discriminator ("first-wins, cancel siblings"). | L | Workflow-pattern conformance tests for each merge type pass. |
| M3.3 | **Cancellation regions** — cancel outstanding parallel branches on discriminator fire. | M | Losing branch tokens are provably removed. |

---

## M4 — Static validation / workflow checker (🟡) — target: **v0.8.0**

Petri-net math is the differentiator; make the "deadlock-free" claim real and *checkable*.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M4.1 | **Reachability / coverability analysis** — detect unreachable places & dead transitions. | L | Checker flags a deliberately dead transition. |
| M4.2 | **Deadlock detection** — report markings with no enabled transition (non-final). | L | Checker flags a hand-built deadlock before deploy. |
| M4.3 | **`workflow lint` CLI + CI hook** — validate YAML definitions pre-deploy. | M | `go run ./cmd/workflow lint file.yaml` exits non-zero on defects. |

---

## M5 — Nested workflows / HCPN (🟡) — target: **v0.9.0**

Depends on CPN (M2) for token passing across levels.

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M5.1 | **Sub-workflow definition & substitution** — a place/transition expands to a nested net. | XL | Order→Payment sub-workflow example (README §HCPN) runs end-to-end. |
| M5.2 | **Token passing across boundaries** — in/out socket mapping between parent and child. | L | Tokens flow parent→child→parent with correct data. |
| M5.3 | **Hierarchical diagram** — Mermaid renders collapse/expand of sub-processes. | M | Diagram shows both summary and drill-down. |
| M5.4 | **Reusable fragment registry** — define once, embed in many parents. | M | One fragment reused in two parent workflows in tests. |

---

## M6 — Time, scheduling & compensation (🟡) — target: **v0.10.0**

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M6.1 | **Timed transitions (TPN/DPN)** — earliest/latest firing windows, fixed durations. | L | "Wait 30 min then fire" and "timeout after 24h" both honored. |
| M6.2 | **Restart-safe scheduler** — pluggable queue adapter (Asynq/NATS/RabbitMQ), timers survive restart. | L | Scheduled transition fires after simulated process restart. |
| M6.3 | **Compensation / rollback** — record completed steps; structured, ordered undo (saga-style). | L | Failed step N triggers reverse compensation of steps N-1…1. |
| M6.4 | **Message correlation** — instances wait for/emit signals keyed by shared data. | L | Workflow A blocks until Workflow B emits correlated signal. |

---

## M7 — Enterprise & governance (🟢) — target: **v0.11.0**

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M7.1 | **Workflow definition versioning & migration** — run old instances on old defs, migrate forward. | L | v1 instance completes after v2 def deployed. |
| M7.2 | **Variable scope management** — local (per-task) vs global (workflow) isolation. | M | Local var invisible to sibling task; global shared. |
| M7.3 | **RBAC** — first-class roles/permissions model beyond guard helpers. | M | Transition denied for unauthorized role with typed error. |
| M7.4 | **Templates system** — parameterized reusable workflow templates. | M | Instantiate two workflows from one template with different params. |
| M7.5 | **Statistics & analytics** — per-transition timing, throughput, bottleneck report. | M | Query "avg time in `review`" returns real data. |

---

## M8 — Interop, API & observability (🟢) — target: **v1.0.0-rc**

| # | Task | Effort | Acceptance |
|---|------|--------|-----------|
| M8.1 | **YAML round-trip** — export Go definitions back to YAML. | M | Load→export→load is idempotent. |

> **M8.1 implementation note (file writing):** when serializing definitions to disk,
> stream directly to the file with `yaml.NewEncoder(file)` (`SetIndent(2)`) or an
> `*json.Encoder`, rather than building the whole document in memory with
> `MarshalIndent` and then writing. This keeps memory flat for large exports. Mirror
> the pattern already used in the author's `apispec` `writeOutput` helper: open with
> `O_WRONLY|O_CREATE|O_TRUNC`, `defer` a checked `Close()`, and on encode error still
> `Close()` the encoder and wrap both errors with `%w`. Default output to stdout when
> no explicit `--output` flag is set; pick YAML vs JSON by file extension.
| M8.2 | **PNML import/export** — interop with standard Petri-net tools. | L | Round-trips a PNML file through an external tool. |
| M8.3 | **REST API** — standalone service over the engine (create/apply/query/history). | L | Documented OpenAPI; example server + integration tests. |
| M8.4 | **Standalone web UI** — promote the example into a real, packaged UI. | L | Runnable binary, not just an example. |
| M8.5 | **OpenTelemetry** — spans around transitions, metrics for firings/latency/errors. | M | Traces visible in an OTel collector; documented. |

**v1.0.0 gate:** every README feature is implemented, tested (≥90%), documented, and
benchmarked; two storage backends; validation, durability, and observability in place;
the "not production ready" banner is removed and the README rewritten to describe only
shipping behavior.

---

## Suggested sequencing rationale

1. **M0 first, always** — honesty + hardening is cheap and de-risks everything else.
2. **M1 before M2** — durable storage underpins tokens, sagas, timers.
3. **M2 is the spine** — CPN unlocks M3 (weighted/sync), M5 (HCPN), M6 (compensation).
4. **M4 validation** can slot in anytime after M2 but pairs well as a mid-project quality gate.
5. **M7–M8** are adoption/interop polish; valuable but not blockers for a defensible 1.0.

## Cross-cutting definition of done (applies to every task)

- Unit + integration tests, ≥90% package coverage, `-race` clean.
- Godoc on all new exported symbols; user-facing docs updated in the same PR.
- No feature merged whose docs/examples can't actually be executed by the engine.
- CHANGELOG entry; benchmark added for any hot path.
