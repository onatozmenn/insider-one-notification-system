package tracing

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Setup configures OTLP HTTP tracing when endpoint is set.
// It returns a no-op shutdown when tracing is disabled.
func Setup(ctx context.Context, endpoint, service, version string) (func(context.Context) error, error) {
	noop := func(context.Context) error { return nil }

	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return noop, nil
	}

	options := make([]otlptracehttp.Option, 0, 2)
	if strings.Contains(endpoint, "://") {
		parsed, err := endpointURL(endpoint)
		if err != nil {
			return noop, err
		}
		options = append(options, otlptracehttp.WithEndpointURL(parsed.String()))
		if parsed.Scheme == "http" {
			options = append(options, otlptracehttp.WithInsecure())
		}
	} else {
		options = append(options, otlptracehttp.WithEndpoint(endpoint), otlptracehttp.WithInsecure())
	}

	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return noop, fmt.Errorf("create OTLP exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithResource(resource.NewSchemaless(
			attribute.String("service.name", service),
			attribute.String("service.version", version),
		)),
	)

	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return provider.Shutdown, nil
}

// endpointURL adds the OTLP HTTP default path when the endpoint omits one.
// WithEndpointURL sets the path verbatim, and an empty path would post to "/".
func endpointURL(endpoint string) (*url.URL, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse OTLP endpoint: %w", err)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1/traces"
	}
	return parsed, nil
}
