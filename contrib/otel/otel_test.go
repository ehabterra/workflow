package otelworkflow_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ehabterra/workflow"
	otelworkflow "github.com/ehabterra/workflow/contrib/otel"
)

// memStore is a minimal in-memory workflow.Storage for driving a Manager.
type memStore struct {
	mu       sync.Mutex
	markings map[string]workflow.Marking
	contexts map[string]map[string]any
	versions map[string]int64
}

func newMemStore() *memStore {
	return &memStore{
		markings: map[string]workflow.Marking{},
		contexts: map[string]map[string]any{},
		versions: map[string]int64{},
	}
}

func (s *memStore) LoadState(ctx context.Context, id string) (workflow.Marking, map[string]any, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.markings[id]
	if !ok {
		return nil, nil, 0, fmt.Errorf("%w: %s", workflow.ErrWorkflowNotFound, id)
	}
	out := workflow.NewMarking(nil)
	for p, toks := range m.AllTokens() {
		if len(toks) == 0 {
			_ = out.AddPlace(p)
		}
		for _, t := range toks {
			out.AddToken(p, t)
		}
	}
	c := map[string]any{}
	for k, v := range s.contexts[id] {
		c[k] = v
	}
	return out, c, s.versions[id], nil
}

func (s *memStore) SaveState(ctx context.Context, id string, m workflow.Marking, c map[string]any, expected int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.versions[id] != expected {
		return 0, fmt.Errorf("%w: %s", workflow.ErrConflict, id)
	}
	s.markings[id] = m
	s.contexts[id] = c
	s.versions[id] = expected + 1
	return s.versions[id], nil
}

func (s *memStore) DeleteState(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.markings, id)
	return nil
}

// guardedNet: in --go--> out (guard: allowed==true), in --always--> out.
func guardedNet(t *testing.T) *workflow.Definition {
	t.Helper()
	guarded := workflow.MustNewTransition("go", []workflow.Place{"in"}, []workflow.Place{"out"})
	gc, err := workflow.NewExpressionConstraint("getContext('allowed', false) == true")
	if err != nil {
		t.Fatal(err)
	}
	guarded.AddConstraint(gc)
	always := workflow.MustNewTransition("always", []workflow.Place{"in"}, []workflow.Place{"out"})
	def, err := workflow.NewDefinition([]workflow.Place{"in", "out"}, []workflow.Transition{*guarded, *always})
	if err != nil {
		t.Fatal(err)
	}
	return def
}

// firingsSum collects the workflow.firings counter datapoints keyed by the
// result attribute, from a manual reader.
func firingsSum(t *testing.T, reader *sdkmetric.ManualReader) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "workflow.firings" {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("workflow.firings data type = %T, want Sum[int64]", m.Data)
			}
			for _, dp := range sum.DataPoints {
				result, _ := dp.Attributes.Value(attribute.Key("workflow.result"))
				out[result.AsString()] += dp.Value
			}
		}
	}
	return out
}

// TestInstrument_SpanAndCounter: a completed firing produces one
// "workflow.fire" span (start = before-event, end after it, attributes
// carrying identity) and one applied datapoint; a guard rejection produces a
// guard_rejected datapoint and no span.
func TestInstrument_SpanAndCounter(t *testing.T) {
	ctx := context.Background()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	def := guardedNet(t)
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore())
	inst, err := otelworkflow.Instrument(mgr,
		otelworkflow.WithTracerProvider(tp),
		otelworkflow.WithMeterProvider(mp))
	if err != nil {
		t.Fatalf("Instrument: %v", err)
	}
	defer inst.Close()

	if _, err := mgr.CreateWorkflow(ctx, "wf-1", def, "in"); err != nil {
		t.Fatal(err)
	}

	// A guard rejection first (marking untouched), then a completed firing.
	err = mgr.Execute(ctx, "wf-1", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "go")
	})
	if !errors.Is(err, workflow.ErrGuardRejected) {
		t.Fatalf("guarded fire = %v, want ErrGuardRejected", err)
	}
	if err := mgr.Execute(ctx, "wf-1", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "always")
	}); err != nil {
		t.Fatalf("Execute(always): %v", err)
	}

	// Exactly one span, for the completed firing.
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1 (rejections produce no span)", len(spans))
	}
	span := spans[0]
	if span.Name() != "workflow.fire" {
		t.Errorf("span name = %q, want workflow.fire", span.Name())
	}
	if !span.EndTime().After(span.StartTime()) {
		t.Errorf("span end %v must be after start %v", span.EndTime(), span.StartTime())
	}
	want := map[attribute.Key]string{
		"workflow.name":       "wf-1",
		"workflow.transition": "always",
		"workflow.from":       "in",
		"workflow.to":         "out",
	}
	got := map[attribute.Key]string{}
	for _, kv := range span.Attributes() {
		got[kv.Key] = kv.Value.AsString()
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("span attr %s = %q, want %q", k, got[k], v)
		}
	}

	// The counter saw both outcomes.
	sums := firingsSum(t, reader)
	if sums["applied"] != 1 || sums["guard_rejected"] != 1 {
		t.Fatalf("firings = %v, want applied:1 guard_rejected:1", sums)
	}
}

// TestInstrument_CloseDetaches: after Close, firings produce neither spans
// nor datapoints.
func TestInstrument_CloseDetaches(t *testing.T) {
	ctx := context.Background()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	def := guardedNet(t)
	mgr := workflow.NewManager(workflow.NewRegistry(), newMemStore())
	inst, err := otelworkflow.Instrument(mgr,
		otelworkflow.WithTracerProvider(tp),
		otelworkflow.WithMeterProvider(mp))
	if err != nil {
		t.Fatal(err)
	}
	inst.Close()

	if _, err := mgr.CreateWorkflow(ctx, "wf-2", def, "in"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Execute(ctx, "wf-2", def, func(wf *workflow.Workflow) error {
		return wf.ApplyTransitionWithContext(ctx, "always")
	}); err != nil {
		t.Fatal(err)
	}

	if n := len(recorder.Ended()); n != 0 {
		t.Fatalf("spans after Close = %d, want 0", n)
	}
	if sums := firingsSum(t, reader); len(sums) != 0 {
		t.Fatalf("datapoints after Close = %v, want none", sums)
	}
}
