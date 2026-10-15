package mcpsrv

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestToolMetricsMiddleware_RecordsCallsAndDuration(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	mw := newToolMetricsMiddleware("petstore")
	okHandler := mw(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{}, nil
	})
	errHandler := mw(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return &mcp.CallToolResult{IsError: true}, nil
	})
	req := &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{Name: "getPet"},
	}
	_, err := okHandler(t.Context(), authzMethodToolsCall, req)
	require.NoError(t, err)
	_, err = okHandler(t.Context(), authzMethodToolsCall, req)
	require.NoError(t, err)
	_, err = errHandler(t.Context(), authzMethodToolsCall, req)
	require.NoError(t, err)
	// tools/call 以外は数えない。
	_, err = okHandler(t.Context(), "tools/list", req)
	require.NoError(t, err)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	counts := map[string]int64{}
	var histCount uint64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Sum[int64]:
				if m.Name != "manifold.tool_calls" {
					continue
				}
				for _, dp := range d.DataPoints {
					server, _ := dp.Attributes.Value("server")
					tool, _ := dp.Attributes.Value("tool")
					status, _ := dp.Attributes.Value("status")
					counts[server.AsString()+"/"+tool.AsString()+"/"+status.AsString()] = dp.Value
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					histCount += dp.Count
				}
			}
		}
	}
	require.Equal(t, map[string]int64{"petstore/getPet/ok": 2, "petstore/getPet/error": 1}, counts)
	require.Equal(t, uint64(3), histCount)
}
