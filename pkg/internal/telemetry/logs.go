package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

func NewLoggerProvider(ctx context.Context, opt *Config) (log.LoggerProvider, func(), error) {
	l := &opt.Logs
	if !l.Enabled {
		return noop.NewLoggerProvider(), func() {}, nil
	}

	var (
		exporter sdklog.Exporter
		err      error
	)
	switch {
	case l.HTTP != nil:
		exporter, err = newHTTPLogExporter(ctx, &opt.Logs, opt.GzipCompression)
	case l.GRPC != nil:
		exporter, err = newGRPCLogExporter(ctx, &opt.Logs, opt.GzipCompression)
	default:
		slog.Warn("logs enabled but HTTP/gRPC endpoint is not configured, disabling logs")
		return noop.NewLoggerProvider(), func() {}, nil
	}
	if err != nil {
		return nil, nil, err
	}

	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(newResource(opt.ServiceName, version.Version, opt.Environment)),
	)
	cleanup := func() { //nolint: contextcheck
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := loggerProvider.Shutdown(ctx); err != nil {
			slog.Warn(err.Error())
		}
	}
	global.SetLoggerProvider(loggerProvider)
	return loggerProvider, cleanup, nil
}

func newHTTPLogExporter(ctx context.Context, opt *LogsConfig, gzipCompression bool) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{}
	endpoint := opt.HTTP.Endpoint
	switch {
	case endpoint.Endpoint != "":
		opts = append(opts, otlploghttp.WithEndpoint(endpoint.Endpoint))
	case endpoint.EndpointURL != "":
		opts = append(opts, otlploghttp.WithEndpointURL(endpoint.EndpointURL))
	default:
		return nil, errors.New("please specify the endpoint or endpoint URL")
	}
	if gzipCompression {
		opts = append(opts, otlploghttp.WithCompression(otlploghttp.GzipCompression))
	}
	return otlploghttp.New(ctx, opts...)
}

func newGRPCLogExporter(ctx context.Context, opt *LogsConfig, gzipCompression bool) (sdklog.Exporter, error) {
	opts := make([]otlploggrpc.Option, 0, 10)
	grpc := opt.GRPC
	if grpc.Insecure {
		opts = append(opts, otlploggrpc.WithInsecure())
	}
	endpoint := grpc.Endpoint
	switch {
	case endpoint.Endpoint != "":
		opts = append(opts, otlploggrpc.WithEndpoint(endpoint.Endpoint))
	case endpoint.EndpointURL != "":
		opts = append(opts, otlploggrpc.WithEndpointURL(endpoint.EndpointURL))
	default:
		return nil, errors.New("please specify the endpoint or endpoint URL")
	}
	if gzipCompression {
		opts = append(opts, otlploggrpc.WithCompressor("gzip"))
	}
	return otlploggrpc.New(ctx, opts...)
}
