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

func NewTracerProvider(ctx context.Context, opt *Config) (trace.TracerProvider, context.CancelFunc, error) {
	if !opt.Enabled {
		return noop.NewTracerProvider(), func() {}, nil
	}

	var (
		traceClient otlptrace.Client
		err         error
	)
	switch {
	case opt.HTTP != nil:
		traceClient, err = httpTrace(ctx, opt)
	case opt.GRPC != nil:
		traceClient, err = grpcTrace(ctx, opt)
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
		f := func(fn func(ctx context.Context) error) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := fn(ctx); err != nil {
				slog.Error(err.Error())
			}
			cancel()
		}
		f(sdkTP.ForceFlush)
		f(sdkTP.Shutdown)
		f(exporter.Shutdown)
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

func httpTrace(ctx context.Context, opt *Config) (otlptrace.Client, error) {
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
	if opt.GzipCompression {
		traceOpts = append(traceOpts, otlptracehttp.WithCompression(otlptracehttp.GzipCompression))
	}
	traceClient := otlptracehttp.NewClient(traceOpts...)
	return traceClient, nil
}

func grpcTrace(ctx context.Context, opt *Config) (otlptrace.Client, error) {
	traceOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithInsecure(),
	}
	endpoint := opt.GRPC.Endpoint
	switch {
	case endpoint.Endpoint != "":
		traceOpts = append(traceOpts, otlptracegrpc.WithEndpoint(endpoint.Endpoint))
	case endpoint.EndpointURL != "":
		traceOpts = append(traceOpts, otlptracegrpc.WithEndpointURL(endpoint.EndpointURL))
	default:
		return nil, errors.New("please specify the endpoint or endpoint URL")
	}
	if opt.GzipCompression {
		traceOpts = append(traceOpts, otlptracegrpc.WithCompressor("gzip"))
	}
	traceClient := otlptracegrpc.NewClient(traceOpts...)
	return traceClient, nil
}
