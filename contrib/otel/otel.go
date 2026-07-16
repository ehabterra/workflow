// Copyright (c) 2026 Ehab Terra
// SPDX-License-Identifier: MIT

// Package otelworkflow instruments a workflow Manager with OpenTelemetry
// traces and metrics. It is built entirely on the library's observer
// listeners (workflow.ObserverFunc), so instrumentation can never error,
// panic outward, or otherwise block a firing — the library's contract for
// observability.
//
// Attach it once, next to where the Manager is built:
//
//	inst, err := otelworkflow.Instrument(mgr)
//	if err != nil { ... }
//	defer inst.Close()
//
// Every completed firing produces a span named "workflow.fire" — parented on
// the context the transition was applied with, so it nests under the caller's
// HTTP/cron span — whose start time is the before-transition event and whose
// attributes carry the workflow name, transition, and from/to places. The
// counter "workflow.firings" counts firing outcomes with attributes
// workflow.name, workflow.transition, and workflow.result ("applied" or
// "guard_rejected").
//
// Providers default to the globals (otel.GetTracerProvider /
// otel.GetMeterProvider); override with WithTracerProvider / WithMeterProvider.
package otelworkflow

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/ehabterra/workflow"
)

// scopeName is the instrumentation scope for the tracer and meter.
const scopeName = "github.com/ehabterra/workflow/contrib/otel"

// staleAfter bounds the in-flight start-time table: a before-event whose
// firing never completed (the apply failed re-verification, or errored
// between the events) is pruned after this long, so the table cannot grow
// without bound. Such attempts simply produce no span.
const staleAfter = 5 * time.Minute

type config struct {
	tracerProvider trace.TracerProvider
	meterProvider  metric.MeterProvider
}

// Option configures Instrument.
type Option func(*config)

// WithTracerProvider sets the TracerProvider (default: the otel global).
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(c *config) { c.tracerProvider = tp }
}

// WithMeterProvider sets the MeterProvider (default: the otel global).
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(c *config) { c.meterProvider = mp }
}

// Instrumentation holds the observers Instrument registered; Close removes
// them from the Manager.
type Instrumentation struct {
	mgr     *workflow.Manager
	handles []*workflow.ListenerHandle

	tracer  trace.Tracer
	firings metric.Int64Counter

	// inflight maps "workflow\x00transition" to the start times of firings
	// whose before-event has run but whose after-event hasn't. Concurrent
	// same-key firings pair LIFO — a rare collision skews a span's start
	// time, never correctness.
	mu       sync.Mutex
	inflight map[string][]time.Time
}

// Instrument attaches OpenTelemetry tracing and metrics to every workflow
// the Manager touches, via non-blocking observers. Call Close to detach.
func Instrument(mgr *workflow.Manager, opts ...Option) (*Instrumentation, error) {
	cfg := config{
		tracerProvider: otel.GetTracerProvider(),
		meterProvider:  otel.GetMeterProvider(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	firings, err := cfg.meterProvider.Meter(scopeName).Int64Counter("workflow.firings",
		metric.WithDescription("Transition firing outcomes (applied or guard_rejected)"),
		metric.WithUnit("{firing}"))
	if err != nil {
		return nil, err
	}

	in := &Instrumentation{
		mgr:      mgr,
		tracer:   cfg.tracerProvider.Tracer(scopeName),
		firings:  firings,
		inflight: make(map[string][]time.Time),
	}
	in.handles = append(in.handles,
		mgr.AddObserver(workflow.EventBeforeTransition, in.onBefore),
		mgr.AddObserver(workflow.EventAfterTransition, in.onAfter),
		mgr.AddObserver(workflow.EventGuardRejected, in.onRejected),
	)
	return in, nil
}

// Close removes the instrumentation's observers from the Manager.
func (in *Instrumentation) Close() {
	for _, h := range in.handles {
		in.mgr.RemoveListener(h)
	}
	in.handles = nil
}

// eventKey identifies a firing attempt for before/after pairing.
func eventKey(e workflow.Event) string {
	name := ""
	if wf := e.Workflow(); wf != nil {
		name = wf.Name()
	}
	tname := ""
	if t := e.Transition(); t != nil {
		tname = t.Name()
	}
	return name + "\x00" + tname
}

func (in *Instrumentation) onBefore(e workflow.Event) {
	now := time.Now()
	key := eventKey(e)
	in.mu.Lock()
	defer in.mu.Unlock()
	// Prune this key's stale entries (firings that never completed) so the
	// table stays bounded without a background goroutine.
	starts := in.inflight[key]
	for len(starts) > 0 && now.Sub(starts[0]) > staleAfter {
		starts = starts[1:]
	}
	in.inflight[key] = append(starts, now)
}

// takeStart pops the most recent in-flight start for the key (LIFO), or
// returns ok=false when none is pending (e.g. the before-event predates
// Instrument, or was pruned).
func (in *Instrumentation) takeStart(key string) (time.Time, bool) {
	in.mu.Lock()
	defer in.mu.Unlock()
	starts := in.inflight[key]
	if len(starts) == 0 {
		return time.Time{}, false
	}
	start := starts[len(starts)-1]
	if len(starts) == 1 {
		delete(in.inflight, key)
	} else {
		in.inflight[key] = starts[:len(starts)-1]
	}
	return start, true
}

func (in *Instrumentation) onAfter(e workflow.Event) {
	end := time.Now()
	start, ok := in.takeStart(eventKey(e))
	if !ok {
		start = end
	}

	ctx := e.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	attrs := firingAttrs(e)

	// The span is created at completion with the before-event's timestamp as
	// its start, so an attempt that never completes cannot leak a live span.
	_, span := in.tracer.Start(ctx, "workflow.fire",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithTimestamp(start),
		trace.WithAttributes(append(attrs,
			attribute.String("workflow.from", placesAttr(e.From())),
			attribute.String("workflow.to", placesAttr(e.To())),
		)...))
	span.End(trace.WithTimestamp(end))

	in.firings.Add(ctx, 1, metric.WithAttributes(append(attrs,
		attribute.String("workflow.result", "applied"))...))
}

func (in *Instrumentation) onRejected(e workflow.Event) {
	ctx := e.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	in.firings.Add(ctx, 1, metric.WithAttributes(append(firingAttrs(e),
		attribute.String("workflow.result", "guard_rejected"))...))
}

// firingAttrs returns the identifying attributes shared by the span and the
// counter: workflow name and transition name.
func firingAttrs(e workflow.Event) []attribute.KeyValue {
	name := ""
	if wf := e.Workflow(); wf != nil {
		name = wf.Name()
	}
	tname := ""
	if t := e.Transition(); t != nil {
		tname = t.Name()
	}
	return []attribute.KeyValue{
		attribute.String("workflow.name", name),
		attribute.String("workflow.transition", tname),
	}
}

func placesAttr(places []workflow.Place) string {
	out := make([]string, len(places))
	for i, p := range places {
		out[i] = string(p)
	}
	return strings.Join(out, ",")
}
