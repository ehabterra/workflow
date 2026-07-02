# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
- `ROADMAP.md` describing the phased path (M0–M8) to a production-ready v1.0.
- Strict YAML decoding: unknown keys in a workflow config are now rejected with a
  line number instead of being silently ignored.
- `.golangci.yml` and a golangci-lint CI job.
- CI job that builds and vets every example module (including nested-module examples).
- `context.Context` is now the first parameter of every `Storage`, `HistoryStore`,
  and `Manager` persistence method; the SQLite implementations honor cancellation.
- Community files: `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `SECURITY.md`, and
  GitHub issue/PR templates.

### Changed
- Minimum Go version raised to 1.24 (module and CI).
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
