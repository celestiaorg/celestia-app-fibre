package fibre

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerEnvKey   = "OTEL_TRACING_ADDRESS"
	tracerName     = "fibre-server"
	tracerLogField = "otel_endpoint"
)

// newServerTracer configures an OTLP HTTP exporter-backed tracer when the OTEL_TRACING_ADDRESS
// environment variable is set. Returning nil means tracing should fall back to the default tracer.
func newServerTracer(ctx context.Context, logger *slog.Logger) (trace.Tracer, func(context.Context) error, error) {
	endpoint := strings.TrimSpace(os.Getenv(tracerEnvKey))
	if endpoint == "" {
		return nil, nil, nil
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", tracerName),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	if logger != nil {
		logger.Info("configured OTLP tracing for fibre server", tracerLogField, endpoint)
	}

	return tp.Tracer(tracerName), tp.Shutdown, nil
}
