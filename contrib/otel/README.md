# contrib/otel — OpenTelemetry instrumentation for the workflow Manager

A separate Go module (so the core library stays dependency-free) that
attaches OpenTelemetry **traces and metrics** to every workflow a `Manager`
touches. It is built entirely on the library's observer listeners
(`workflow.ObserverFunc`), which can never error, panic outward, or block a
firing — instrumentation stays out of the business flow by construction.

```go
import otelworkflow "github.com/ehabterra/workflow/contrib/otel"

inst, err := otelworkflow.Instrument(mgr)
if err != nil { ... }
defer inst.Close()
```

Providers default to the OTel globals; override with
`WithTracerProvider` / `WithMeterProvider`.

## What you get

**Spans** — every completed firing produces a `workflow.fire` span:

- parented on the context the transition was applied with, so it nests under
  the caller's HTTP handler or cron span;
- its start time is the before-transition event, its end the
  after-transition event (the span is created at completion, so an attempt
  that never completes cannot leak a live span);
- attributes: `workflow.name`, `workflow.transition`, `workflow.from`,
  `workflow.to`.

**Metrics** — the counter `workflow.firings` (unit `{firing}`) counts firing
outcomes with attributes `workflow.name`, `workflow.transition`, and
`workflow.result`:

- `applied` — the transition fired and moved the marking;
- `guard_rejected` — a guard (expression constraint or blocking guard
  listener) refused the attempt, observed via the library's
  observability-only `EventGuardRejected`.

## Seeing it live

The reference system wires this up behind one flag:

```sh
# terminal 1: any OTLP/HTTP collector, e.g.
docker run --rm -p 4318:4318 otel/opentelemetry-collector

# terminal 2:
cd examples/expense_approval
go run . -db expenses.db -otel-endpoint localhost:4318
```

Submit an expense and approve it: each webhook's HTTP request produces a
trace with the `workflow.fire` spans nested inside it, and the collector
receives `workflow.firings` counts per transition and outcome.
