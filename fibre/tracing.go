package fibre

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerEnvKey      = "OTEL_TRACING_ADDRESS"
	serverTracerName  = "fibre-server"
	clientTracerName  = "fibre-client"
	tracerLogField    = "otel_endpoint"
	tracerName        = serverTracerName // for backward compatibility
)

// newServerTracer configures an OTLP gRPC exporter-backed tracer when the OTEL_TRACING_ADDRESS
// environment variable is set. Returning nil means tracing should fall back to the default tracer.
func newServerTracer(ctx context.Context, logger *slog.Logger, chainID string) (trace.Tracer, func(context.Context) error, error) {
	// Hardcoded endpoint for tracing - port 4317 is OTLP gRPC for Jaeger
	endpoint := "137.184.170.98:4317"

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", serverTracerName),
	}
	if chainID != "" {
		attrs = append(attrs, attribute.String("chain.id", chainID))
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	if logger != nil {
		logger.Info("configured OTLP tracing for fibre server", tracerLogField, endpoint, "chain_id", chainID)
	}

	return tp.Tracer(serverTracerName), tp.Shutdown, nil
}

// newClientTracer configures an OTLP gRPC exporter-backed tracer for the client.
func newClientTracer(ctx context.Context, logger *slog.Logger, chainID string) (trace.Tracer, func(context.Context) error, error) {
	// Hardcoded endpoint for tracing - port 4317 is OTLP gRPC for Jaeger
	endpoint := "137.184.170.98:4317"

	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP exporter: %w", err)
	}

	attrs := []attribute.KeyValue{
		attribute.String("service.name", clientTracerName),
	}
	if chainID != "" {
		attrs = append(attrs, attribute.String("chain.id", chainID))
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("creating OTLP resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	if logger != nil {
		logger.Info("configured OTLP tracing for fibre client", tracerLogField, endpoint, "chain_id", chainID)
	}

	return tp.Tracer(clientTracerName), tp.Shutdown, nil
}
