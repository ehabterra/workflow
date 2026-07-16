// OpenTelemetry wiring for the demo, enabled by -otel-endpoint: the
// contrib/otel instrumentation attached to the app's Manager, exporting
// traces and metrics over OTLP/HTTP to a collector. This is the M5.3
// acceptance in the flesh — "traces visible in a collector from the
// reference system".
package main

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/ehabterra/workflow"
	otelworkflow "github.com/ehabterra/workflow/contrib/otel"
)

// setupOTel instruments the Manager with contrib/otel and exports spans and
// the workflow.firings counter to the OTLP/HTTP collector at endpoint
// (host:port; plain HTTP — a local demo collector, not production TLS). The
// returned shutdown detaches the instrumentation and flushes the providers.
func setupOTel(ctx context.Context, endpoint string, mgr *workflow.Manager) (func(context.Context) error, error) {
	res, err := resource.Merge(resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName("expense-approval")))
	if err != nil {
		return nil, err
	}

	traceExp, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure())
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp), sdktrace.WithResource(res))

	metricExp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint(endpoint), otlpmetrichttp.WithInsecure())
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res))

	inst, err := otelworkflow.Instrument(mgr,
		otelworkflow.WithTracerProvider(tp), otelworkflow.WithMeterProvider(mp))
	if err != nil {
		_ = errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
		return nil, err
	}

	// Globals too, so any future instrumentation in the app picks them up.
	// Set only after full success, so a partial init never leaves process-wide
	// providers pointed at something nobody will shut down.
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)

	return func(ctx context.Context) error {
		inst.Close()
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}
