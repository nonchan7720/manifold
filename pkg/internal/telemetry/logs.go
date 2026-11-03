package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log"
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
	otel.SetLoggerProvider(loggerProvider)
	return loggerProvider, cleanup, nil
}

// logHTTPOptions / logGRPCOptions は共通設定を otlploghttp / otlploggrpc のオプションに変換する。
var (
	logHTTPOptions = otlpOptionFuncs[otlploghttp.Option]{
		endpoint:    otlploghttp.WithEndpoint,
		endpointURL: otlploghttp.WithEndpointURL,
		gzip: func() otlploghttp.Option {
			return otlploghttp.WithCompression(otlploghttp.GzipCompression)
		},
		headers: otlploghttp.WithHeaders,
	}
	logGRPCOptions = otlpOptionFuncs[otlploggrpc.Option]{
		insecure:    otlploggrpc.WithInsecure,
		endpoint:    otlploggrpc.WithEndpoint,
		endpointURL: otlploggrpc.WithEndpointURL,
		gzip:        func() otlploggrpc.Option { return otlploggrpc.WithCompressor("gzip") },
		headers:     otlploggrpc.WithHeaders,
	}
)

func newHTTPLogExporter(
	ctx context.Context,
	opt *LogsConfig,
	gzipCompression bool,
) (sdklog.Exporter, error) {
	opts, err := logHTTPOptions.buildHTTP(opt.HTTP, gzipCompression)
	if err != nil {
		return nil, err
	}
	return otlploghttp.New(ctx, opts...)
}

func newGRPCLogExporter(
	ctx context.Context,
	opt *LogsConfig,
	gzipCompression bool,
) (sdklog.Exporter, error) {
	opts, err := logGRPCOptions.buildGRPC(opt.GRPC, gzipCompression)
	if err != nil {
		return nil, err
	}
	return otlploggrpc.New(ctx, opts...)
}
