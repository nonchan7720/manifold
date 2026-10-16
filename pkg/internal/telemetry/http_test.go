package telemetry_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// activeRequests は現在の http.server.active_requests の合計を返す。
func activeRequests(t *testing.T, reader sdkmetric.Reader) (total int64, found bool) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != telemetry.ActiveRequestsMetricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok)
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
			found = true
		}
	}
	return total, found
}

func TestActiveRequests_CountsInFlight(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	entered := make(chan struct{})
	release := make(chan struct{})
	h := telemetry.ActiveRequests(mp, func(r *http.Request) bool {
		return r.URL.Path == "/healthz"
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(entered)
			<-release
		}
	}))

	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(httptest.NewRecorder(),
			httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/slow", nil))
	}()
	<-entered

	total, found := activeRequests(t, reader)
	require.True(t, found)
	require.Equal(t, int64(1), total)

	// スキップ対象のリクエストは数えない。
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
	total, _ = activeRequests(t, reader)
	require.Equal(t, int64(1), total)

	close(release)
	<-done
	total, _ = activeRequests(t, reader)
	require.Equal(t, int64(0), total)
}
