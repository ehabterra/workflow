# Contributing to Go Workflow

Thanks for your interest in improving Go Workflow! This project is a Petri-net
workflow engine for Go, and we welcome issues, discussion, and pull requests.

Please read [`ROADMAP.md`](ROADMAP.md) first — it explains what is implemented today
versus what is planned, and which milestone a given feature belongs to. Anything under
[`docs/roadmap/`](docs/roadmap/) is a **design target, not yet implemented**.

## Getting started

```bash
git clone https://github.com/ehabterra/workflow
cd workflow
go test ./...
```

Requirements:
- Go 1.24 or newer.
- [golangci-lint](https://golangci-lint.run/) v2 for linting (`golangci-lint run ./...`).

## Development workflow

1. Open (or comment on) an issue describing the change, so we can agree on the approach.
2. Create a branch off `main`.
3. Make your change with tests. New code should keep package coverage at or above the
   current level (core is ~90%+).
4. Before pushing, make sure the full local gate passes:
   ```bash
   gofmt -s -l .        # must print nothing
   go vet ./...
   golangci-lint run ./...
   go test -race ./...
   ```
   For changes to an example module, also build it: `(cd examples/<name> && go build ./... && go vet ./...)`.
5. Add a `CHANGELOG.md` entry under **[Unreleased]**.
6. Open a pull request and fill in the template.

## Standards

- **Docs and code must agree.** Do not document or add examples for a feature the engine
  cannot actually execute. If it's planned, put it under `docs/roadmap/` and mark it as
  such.
- Wrap errors with `%w`; prefer `errors.Is`/`errors.As` over `==` comparisons.
- Public APIs take a `context.Context` as their first argument where they do I/O.
- Exported symbols need godoc comments.

## Reporting bugs and requesting features

Use the GitHub issue templates. For security issues, follow [`SECURITY.md`](SECURITY.md)
instead of opening a public issue.

## License

By contributing, you agree that your contributions are licensed under the project's
[MIT License](LICENSE).
