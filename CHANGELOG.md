# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
