package telemetry

import (
	"log/slog"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// ActiveRequestsMetricName は OTel セマンティック規約の http.server.active_requests。
// otelhttp はこのメトリクスを出さないため、ここで補う。
// Prometheus では http_server_active_requests になる。
const ActiveRequestsMetricName = "http.server.active_requests"

// ActiveRequests は処理中の HTTP リクエスト数を UpDownCounter で記録する
// ミドルウェアを返す。skip が true を返すリクエスト（プローブや /metrics など）は数えない。
// 処理中は SSE / WebSocket のような長時間接続も含む。
// I/O バウンドなゲートウェイでは CPU / メモリより同時接続数の方がスケールの指標になる。
func ActiveRequests(
	mp metric.MeterProvider,
	skip func(*http.Request) bool,
) func(http.Handler) http.Handler {
	meter := mp.Meter("github.com/nonchan7720/manifold/pkg/internal/telemetry")
	counter, err := meter.Int64UpDownCounter(
		ActiveRequestsMetricName,
		metric.WithUnit("{request}"),
		metric.WithDescription("Number of in-flight HTTP server requests"),
	)
	if err != nil {
		slog.Warn("failed to create metric", slog.String("metric", ActiveRequestsMetricName),
			slog.Any("error", err))
		counter, _ = noop.NewMeterProvider().Meter("").Int64UpDownCounter(ActiveRequestsMetricName)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) {
				next.ServeHTTP(w, r)
				return
			}
			// 開始時と終了時で同じ属性を使わないと系列が噛み合わない。
			opt := metric.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.scheme", scheme(r)),
			)
			ctx := r.Context()
			counter.Add(ctx, 1, opt)
			defer counter.Add(ctx, -1, opt)
			next.ServeHTTP(w, r)
		})
	}
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
