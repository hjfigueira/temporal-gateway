// Package telemetry configures OpenTelemetry distributed tracing for the
// gateway: an OTLP/gRPC exporter, a resource identifying this service, and
// the process-global TracerProvider/TextMapPropagator every span in the
// process is created through - including the spans internal/gateway starts
// per request and the ones go.temporal.io/sdk/contrib/opentelemetry
// attaches to outgoing Temporal calls (see internal/temporal.NewClient).
package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.32.0"

	"temporal-gateway/internal/config"
)

// Setup configures the process-global TracerProvider and TextMapPropagator
// from cfg and returns a shutdown func that flushes and closes the OTLP
// exporter; callers should defer it. If cfg.Enabled is false, Setup leaves
// the global no-op provider in place and returns a no-op shutdown, so
// callers don't need to branch on whether tracing is on - spans created via
// otel.Tracer(...) are simply discarded.
func Setup(ctx context.Context, cfg config.OTelConfig) (shutdown func(context.Context) error, err error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res := resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(cfg.ServiceName))

	exporterOpts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.Endpoint)}
	if cfg.Insecure {
		exporterOpts = append(exporterOpts, otlptracegrpc.WithInsecure())
	}
	exporter, err := otlptracegrpc.New(ctx, exporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create OTLP exporter for %q: %w", cfg.Endpoint, err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			// Explicit bounds so a slow/unreachable collector caps the
			// in-memory span queue instead of it growing unbounded under
			// sustained request load.
			sdktrace.WithMaxQueueSize(2048),
			sdktrace.WithMaxExportBatchSize(512),
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio(cfg)))),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// sampleRatio returns cfg.SampleRatio, defaulting to 1 (sample everything)
// for an unset or invalid (<= 0) value.
func sampleRatio(cfg config.OTelConfig) float64 {
	if cfg.SampleRatio <= 0 {
		return 1
	}
	return cfg.SampleRatio
}
