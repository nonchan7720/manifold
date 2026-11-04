package telemetry

import (
	"context"
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

// traceHTTPOptions / traceGRPCOptions は共通設定を otlptracehttp / otlptracegrpc のオプションに変換する。
var (
	traceHTTPOptions = otlpOptionFuncs[otlptracehttp.Option]{
		endpoint:    otlptracehttp.WithEndpoint,
		endpointURL: otlptracehttp.WithEndpointURL,
		gzip: func() otlptracehttp.Option {
			return otlptracehttp.WithCompression(otlptracehttp.GzipCompression)
		},
		headers: otlptracehttp.WithHeaders,
	}
	traceGRPCOptions = otlpOptionFuncs[otlptracegrpc.Option]{
		insecure:    otlptracegrpc.WithInsecure,
		endpoint:    otlptracegrpc.WithEndpoint,
		endpointURL: otlptracegrpc.WithEndpointURL,
		gzip:        func() otlptracegrpc.Option { return otlptracegrpc.WithCompressor("gzip") },
		headers:     otlptracegrpc.WithHeaders,
	}
)

func newHTTPTrace(_ context.Context, opt *Trace, gzipCompression bool) (otlptrace.Client, error) {
	opts, err := traceHTTPOptions.buildHTTP(opt.HTTP, gzipCompression)
	if err != nil {
		return nil, err
	}
	return otlptracehttp.NewClient(opts...), nil
}

func newGRPCTrace(_ context.Context, opt *Trace, gzipCompression bool) (otlptrace.Client, error) {
	opts, err := traceGRPCOptions.buildGRPC(opt.GRPC, gzipCompression)
	if err != nil {
		return nil, err
	}
	return otlptracegrpc.NewClient(opts...), nil
}
