# CLAUDE.md — guidance for AI agents working in this repo

This file is for AI coding agents. It captures the conventions, architecture, and
hard-won cautions that are **not** obvious from reading the code. Read it before
making changes. For human-facing contribution rules see [`CONTRIBUTING.md`](CONTRIBUTING.md);
for what is built vs. planned see [`ROADMAP.md`](ROADMAP.md).

## What this project is

`github.com/ehabterra/workflow` is an **embedded, in-process, Petri-net workflow
library** for Go — think "the Symfony Workflow of Go", not a Temporal competitor.
It is a **library, not an engine**: there is no server, no internal scheduler, no
durable-execution replay. The host owns the loop, the clock, and the transactions.
Features that would turn it into a durable-execution server are deliberately out of
scope — each such boundary is documented in [`docs/BOUNDARIES.md`](docs/BOUNDARIES.md)
rather than half-implemented.

The one-line mental model: a workflow instance's state **is its marking** — the set
of places currently holding tokens — not a status column. Applying a transition
consumes tokens from its input places and produces them at its outputs. See
[`docs/guides/MENTAL_MODEL.md`](docs/guides/MENTAL_MODEL.md).

## Module layout

This is a multi-module repo. The **root module** is dependency-light on purpose.

| Path | Module | What |
|---|---|---|
| `.` (root) | `github.com/ehabterra/workflow` | Core: definitions, markings, tokens, transitions, guards, events/listeners, timers, Mermaid diagrams, the `Manager`/`Registry`. |
| `storage/` | root | SQLite + Postgres backends (`database/sql`), optimistic concurrency, the token table, transactional building blocks. |
| `history/` | root | Append-only transition-history store (SQLite + Postgres). |
| `yaml/` | root | YAML config loader + template resolution + storage-setup helpers. |
| `storagetest/` | root | **Conformance kit** — one suite every `Storage` backend must pass (`storagetest.Run`). |
| `workflowtest/` | root | Public test helpers: `AssertMarking`/`AssertHas`, the `Apply` path runner, the `AssertGuard` harness, and a fake `Clock`. |
| `internal/spike/` | root | **Measurement artifacts, not shipped code and not examples.** `approval/` is the declarative-coverage spike (issue #45): a realistic approval workflow built against only what ships, so the value proposition has a number. `internal/` keeps it out of the public API; it stays in the root module so `go test -p 1 ./...` re-runs it and it can't rot. Excluded in `codecov.yml`. Its tests deliberately assert current *defects* — see `COVERAGE.md` before "fixing" one. |
| `contrib/otel/` | **separate** go.mod | OpenTelemetry integration. Kept out of the root module so the core stays dependency-free. |
| `examples/*/` | mostly **separate** go.mod each | Runnable demos (`expense_approval` is the dogfood reference system). |

Because `contrib/otel` and most `examples/*` are **their own modules**, `go test ./...`
from the root does **not** reach them. Test them from their own directory, and remember
they consume the root via a `replace` directive locally but via a version tag when
published — see the MVS caution below.

## Build, test, lint

```bash
gofmt -s -l .          # must print nothing
go vet ./...
golangci-lint run ./... # v2
go build ./...
```

### Running tests — SERIALIZE to avoid overloading the machine

Several `storage`/`history` tests spin up **Postgres via testcontainers** (Docker).
Running packages in parallel can launch many containers at once and wedge the host.
**Always serialize:**

```bash
go test -p 1 ./...
```

- `-p 1` runs one package at a time. Do not drop it.
- Postgres tests use `WORKFLOW_TEST_POSTGRES_DSN` when set (CI supplies a service
  container this way), otherwise start a throwaway testcontainer when Docker is
  available, otherwise **skip**. So a green local run without Docker does *not* mean
  the Postgres paths were exercised.
- To measure coverage: `go test -p 1 -covermode=atomic -coverprofile=cov.out <pkgs>`
  then `go tool cover -func=cov.out`.

### Coverage reality

Core packages sit above 95% (root, `history`, `yaml`, `workflowtest`). `storage` and
`contrib/otel` plateau around ~89%: their residual is **DB-driver-fault and timing
edge branches** (mid-transaction failures, scan errors, stale-entry pruning) that
need a fault-injecting driver to reach. Cheap, legitimate fault injection already in
use: operate on a **closed `*sql.DB`** to exercise query-error branches; seed a
**corrupt row** to exercise decode-error branches. Prefer those over mocking.

## Conventions

- **Branch + PR per change.** Branch off `main`; never commit straight to `main`.
- **Commit trailer.** End commit messages with:
  `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`
- **CHANGELOG + docs.** Add a `CHANGELOG.md` entry under **[Unreleased]** for every
  user-visible change. `CHANGELOG.md` and `ROADMAP.md` conflict on nearly every merge
  because parallel branches all append to them — resolve as the **union** of bullets
  (strip the markers, keep both sides, de-dupe), never by picking one side.
- **Copyright headers.** Every `.go` file starts with a two-line MIT header stamped
  with the file's **git creation year** (2025 for the original core, 2026 for later
  additions). New files must get one too.
- **Comments** state constraints the code can't show; they are not a changelog. Match
  the density and idiom of the surrounding file.
- **The code and the docs must never disagree** — a drift between them is a defect,
  not a docs chore. README snippets are verified to compile/run.

## Architecture & invariants (do not break these)

- **Atomic load → fire → save.** A fire mutates memory only; persistence is a separate,
  version-guarded step. The `Manager.Execute` path retries the *whole* load-fire-save
  cycle on `ErrConflict` (optimistic concurrency), so a side effect passed to it may run
  more than once — it must be idempotent. See the RETRY RESETS godoc on `Manager.Execute`.
- **Optimistic concurrency.** Saves are guarded by the instance's version; a stale writer
  gets `ErrConflict` and the in-memory version is left unchanged so the caller can reload.
- **Empty markings are valid.** A marking with zero marked places is a legitimate state
  (a pure token-pool net is empty between batches). Do not add "must have a place" checks.
- **Token-normalized storage.** The SQL backends persist a marking as one row per token
  in a `<table>_tokens` child table, but the whole-marking overwrite + per-instance version
  are unchanged, so the atomic contract holds. Loads use a single LEFT JOIN (atomic
  snapshot); saves write instance row + token rows in one tx. Legacy JSON-blob rows load
  as-is and normalize on next save; `WithTokenTable("")` opts out.
- **Definition fingerprint + shape.** Every save stamps a fingerprint and a compact
  structural "shape" under reserved context keys. A mismatch on load routes to the
  `WithDefinitionMigration` handler (which gets a `DefinitionDiff`); without a handler it
  is a hard error. Never surface these reserved keys to user code.
- **OR-input / XOR routing.** `from_any: true` enables a transition when *any* one input
  is marked and consumes only that one. `Workflow.ApplyAny(names...)` fires the first
  allowed candidate. Reset arcs (`resets:`) clear whole places when a transition fires.

## Cautions / gotchas (learned the hard way)

- **The workflow lock is a `sync.RWMutex` and is NOT reentrant.** A method already
  holding the lock must call the lock-free `…Locked` variant (e.g. `coloredTokensAtLocked`),
  never the public method — re-entering deadlocks or races. When you refresh a token
  snapshot after re-resolving a consume-set, use the locked variant.
- **Mermaid label escaping uses `#nnn;`, not `&#nnn;`.** `escapeMermaidLabel` emits
  Mermaid's own numeric-entity convention (`#39;` for `'`, `#58;` for `:`, `#34;` for `"`,
  `#60;`/`#62;` for `<`/`>`) — a `#` with **no** ampersand. This is correct and renders as
  the character; do not "fix" it to `&#39;`. (CodeRabbit has flagged this as a false positive.)
- **The ◉ START marker comes from an instance, not a definition.** `Definition.Diagram()`
  is structure-only and has no start marker. To render one, build a fresh instance from the
  YAML `initial_marking` and call `Workflow.Diagram()`. Do not hardcode a start place.
- **`contrib/otel` and the dogfood pin the parent via MVS.** `contrib/otel`'s go.mod
  `require`s a *published version* of the root module; external consumers ignore the local
  `replace`. Bumping that pin raises the minimum for every dependent (the dogfood), which
  can break `Build examples` in CI with "updates to go.mod needed" until you `go mod tidy`
  the dependent. After merging root changes, re-run tests in the example modules, not just
  the root.
- **Never commit build artifacts.** Compiled example binaries and `*.db` files get created
  by running the demos; they are git-ignored — do not `git add -A` them in. A stale
  `advanced_workflow.db` will also cause a fingerprint mismatch on the next demo run.
- **Package doc comments must stay attached to `package`.** When prepending anything (like a
  copyright header) keep a blank line between it and a `// Package …` doc comment so the doc
  stays associated.

## Where to look

- Mental model & recipes: `docs/guides/MENTAL_MODEL.md`, `docs/guides/PRODUCTION_RECIPES.md`.
- What it deliberately does *not* do: `docs/BOUNDARIES.md`.
- Colored tokens, timers: `docs/guides/CPN_GUIDE.md`, `docs/guides/TIMERS_GUIDE.md`.
- Shared-pool / batch modeling design: `docs/roadmap/SHARED_POOL_MODELING.md`.
- The reference system (dogfood): `examples/expense_approval/`, spec in `docs/DOGFOOD.md`.
- Friction log driving priorities: `docs/roadmap/FRICTION.md`.
