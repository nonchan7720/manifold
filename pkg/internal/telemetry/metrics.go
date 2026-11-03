package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/nonchan7720/manifold/pkg/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func NewMeterProvider(
	ctx context.Context,
	opt *Config,
) (metric.MeterProvider, http.Handler, func(), error) {
	m := &opt.Metrics
	if !m.Enabled {
		return noop.NewMeterProvider(), nil, func() {}, nil
	}

	switch m.ExporterType {
	case ExporterTypePull:
		return newPullMeterProvider(ctx, opt)
	case ExporterTypePush:
		return newPushMeterProvider(ctx, opt)
	default:
		slog.Warn("metrics enabled but exporterType is not set, disabling metrics")
		return noop.NewMeterProvider(), nil, func() {}, nil
	}
}

func newPullMeterProvider(
	_ context.Context,
	opt *Config,
) (metric.MeterProvider, http.Handler, func(), error) {
	exporter, err := promexporter.New()
	if err != nil {
		return nil, nil, nil, err
	}
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
		sdkmetric.WithResource(newResource(opt.ServiceName, version.Version, opt.Environment)),
	)
	cleanup := func() { //nolint: contextcheck
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := meterProvider.Shutdown(ctx); err != nil {
			slog.Warn(err.Error())
		}
	}
	otel.SetMeterProvider(meterProvider)
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		return meterProvider, nil, cleanup, err
	}
	return meterProvider, promhttp.Handler(), cleanup, nil
}

func newPushMeterProvider(
	ctx context.Context,
	opt *Config,
) (metric.MeterProvider, http.Handler, func(), error) {
	m := &opt.Metrics
	var (
		exporter sdkmetric.Exporter
		err      error
	)
	switch {
	case m.HTTP != nil:
		exporter, err = newHTTPMetricExporter(ctx, &opt.Metrics, opt.GzipCompression)
	case m.GRPC != nil:
		exporter, err = newGRPCMetricExporter(ctx, &opt.Metrics, opt.GzipCompression)
	default:
		slog.Warn(
			"metrics push enabled but HTTP/gRPC endpoint is not configured, disabling metrics",
		)
		return noop.NewMeterProvider(), nil, func() {}, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		sdkmetric.WithResource(newResource(opt.ServiceName, version.Version, opt.Environment)),
	)
	cleanup := func() { //nolint: contextcheck
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := meterProvider.Shutdown(ctx); err != nil {
			slog.Warn(err.Error())
		}
	}
	otel.SetMeterProvider(meterProvider)
	if err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(time.Second)); err != nil {
		return meterProvider, nil, cleanup, err
	}
	return meterProvider, nil, cleanup, nil
}

// metricHTTPOptions / metricGRPCOptions は共通設定を otlpmetrichttp / otlpmetricgrpc のオプションに変換する。
var (
	metricHTTPOptions = otlpOptionFuncs[otlpmetrichttp.Option]{
		endpoint:    otlpmetrichttp.WithEndpoint,
		endpointURL: otlpmetrichttp.WithEndpointURL,
		gzip: func() otlpmetrichttp.Option {
			return otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression)
		},
		headers: otlpmetrichttp.WithHeaders,
	}
	metricGRPCOptions = otlpOptionFuncs[otlpmetricgrpc.Option]{
		insecure:    otlpmetricgrpc.WithInsecure,
		endpoint:    otlpmetricgrpc.WithEndpoint,
		endpointURL: otlpmetricgrpc.WithEndpointURL,
		gzip:        func() otlpmetricgrpc.Option { return otlpmetricgrpc.WithCompressor("gzip") },
		headers:     otlpmetricgrpc.WithHeaders,
	}
)

func newHTTPMetricExporter(
	ctx context.Context,
	opt *MetricsConfig,
	gzipCompression bool,
) (sdkmetric.Exporter, error) {
	opts, err := metricHTTPOptions.buildHTTP(opt.HTTP, gzipCompression)
	if err != nil {
		return nil, err
	}
	return otlpmetrichttp.New(ctx, opts...)
}

func newGRPCMetricExporter(
	ctx context.Context,
	opt *MetricsConfig,
	gzipCompression bool,
) (sdkmetric.Exporter, error) {
	opts, err := metricGRPCOptions.buildGRPC(opt.GRPC, gzipCompression)
	if err != nil {
		return nil, err
	}
	return otlpmetricgrpc.New(ctx, opts...)
}
