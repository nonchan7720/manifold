package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func NewTracerProvider(ctx context.Context, opt *Config) (trace.TracerProvider, func(), error) {
	t := opt.Trace
	if !t.Enabled {
		return noop.NewTracerProvider(), func() {}, nil
	}

	var (
		traceClient otlptrace.Client
		err         error
	)
	switch {
	case t.HTTP != nil:
		traceClient, err = newHTTPTrace(ctx, &opt.Trace, opt.GzipCompression)
	case t.GRPC != nil:
		traceClient, err = newGRPCTrace(ctx, &opt.Trace, opt.GzipCompression)
	default:
		slog.Warn("enable tracing, configure HTTP or gRPC")
		return noop.NewTracerProvider(), func() {}, nil
	}
	if err != nil {
		return nil, nil, err
	}

	exporter, err := otlptrace.New(ctx, traceClient)
	if err != nil {
		return nil, nil, err
	}

	r := newResource(opt.ServiceName, version.Version, opt.Environment)
	sdkTP := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(r),
	)

	cleanup := func() { //nolint: contextcheck
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := sdkTP.Shutdown(ctx); err != nil {
			slog.Warn(err.Error())
		}
	}

	pp := newPropagator()
	otel.SetTextMapPropagator(pp)
	otel.SetTracerProvider(sdkTP)
	return sdkTP, cleanup, nil
}

func newResource(serviceName string, version string, environment string) *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceVersionKey.String(version),
		semconv.DeploymentEnvironmentNameKey.String(environment),
		attribute.String("environment", environment),
		attribute.String("env", environment),
	)
}

func newPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

func newHTTPTrace(_ context.Context, opt *Trace, gzipCompression bool) (otlptrace.Client, error) {
	traceOpts := []otlptracehttp.Option{}
	endpoint := opt.HTTP.Endpoint
	switch {
	case endpoint.Endpoint != "":
		traceOpts = append(traceOpts, otlptracehttp.WithEndpoint(endpoint.Endpoint))
	case endpoint.EndpointURL != "":
		traceOpts = append(traceOpts, otlptracehttp.WithEndpointURL(endpoint.EndpointURL))
	default:
		return nil, errors.New("please specify the endpoint or endpoint URL")
	}
	if gzipCompression {
		traceOpts = append(traceOpts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	}
	traceClient := otlptracehttp.NewClient(traceOpts...)
	return traceClient, nil
}

func newGRPCTrace(_ context.Context, opt *Trace, gzipCompression bool) (otlptrace.Client, error) {
	opts := make([]otlptracegrpc.Option, 0, 10)
	grpc := opt.GRPC
	if grpc.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	endpoint := grpc.Endpoint
	switch {
	case endpoint.Endpoint != "":
		opts = append(opts, otlptracegrpc.WithEndpoint(endpoint.Endpoint))
	case endpoint.EndpointURL != "":
		opts = append(opts, otlptracegrpc.WithEndpointURL(endpoint.EndpointURL))
	default:
		return nil, errors.New("please specify the endpoint or endpoint URL")
	}
	if gzipCompression {
		opts = append(opts, otlptracegrpc.WithCompressor("gzip"))
	}
	traceClient := otlptracegrpc.NewClient(opts...)
	return traceClient, nil
}
