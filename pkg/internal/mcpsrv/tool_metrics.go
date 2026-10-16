package mcpsrv

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// toolMetrics は tools/call の回数と所要時間のメトリクス。
type toolMetrics struct {
	calls    metric.Int64Counter
	duration metric.Float64Histogram
}

func newToolMetrics(mp metric.MeterProvider) *toolMetrics {
	meter := mp.Meter("github.com/nonchan7720/manifold/pkg/internal/mcpsrv")
	noopMeter := noop.NewMeterProvider().Meter("")
	calls, err := meter.Int64Counter(
		"manifold.tool_calls",
		metric.WithUnit("{call}"),
		metric.WithDescription("tools/call requests, by server, tool and status"),
	)
	if err != nil {
		slog.Warn("failed to create metric", slog.String("metric", "manifold.tool_calls"),
			slog.Any("error", err))
		calls, _ = noopMeter.Int64Counter("manifold.tool_calls")
	}
	duration, err := meter.Float64Histogram(
		"manifold.tool_call.duration",
		metric.WithUnit("s"),
		metric.WithDescription("tools/call duration, by server, tool and status"),
	)
	if err != nil {
		slog.Warn("failed to create metric", slog.String("metric", "manifold.tool_call.duration"),
			slog.Any("error", err))
		duration, _ = noopMeter.Float64Histogram("manifold.tool_call.duration")
	}
	return &toolMetrics{calls: calls, duration: duration}
}

// unknownToolLabel は公開されていないツール名をまとめる tool 属性の値。
const unknownToolLabel = "unknown"

// knownTools は tool 属性に使ってよいツール名の集合。クライアントが任意の名前で
// tools/call できるため、生の名前をそのまま属性にするとカーディナリティが
// 呼び出し側次第で増えてしまう。tools/list の結果に載った名前（＝公開中のツール）
// と、成功した呼び出しの名前だけを覚え、それ以外は unknownToolLabel に集約する。
type knownTools struct {
	names sync.Map // map[string]struct{}
}

func (k *knownTools) learnList(res mcp.Result) {
	list, ok := res.(*mcp.ListToolsResult)
	if !ok || list == nil {
		return
	}
	for _, t := range list.Tools {
		if t != nil {
			k.names.Store(t.Name, struct{}{})
		}
	}
}

func (k *knownTools) label(name string, succeeded bool) string {
	if succeeded {
		k.names.Store(name, struct{}{})
		return name
	}
	if _, ok := k.names.Load(name); ok {
		return name
	}
	return unknownToolLabel
}

// newToolMetricsMiddleware は server に対する全 tools/call の回数・所要時間を
// 記録するミドルウェアを返す。status は、transport エラーまたは isError の結果なら "error"、
// それ以外は "ok"。tool 属性は公開中のツール名か unknownToolLabel（knownTools 参照）。
// グローバル MeterProvider が noop なら何も記録しない。
func newToolMetricsMiddleware(server string) mcp.Middleware {
	m := newToolMetrics(otel.GetMeterProvider())
	known := &knownTools{}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == authzMethodToolsList {
				res, err := next(ctx, method, req)
				if err == nil {
					known.learnList(res)
				}
				return res, err
			}
			if method != authzMethodToolsCall {
				return next(ctx, method, req)
			}
			params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok {
				return next(ctx, method, req)
			}
			start := time.Now()
			res, err := next(ctx, method, req)
			status := "ok"
			if r, isResult := res.(*mcp.CallToolResult); err != nil ||
				(isResult && r != nil && r.IsError) {
				status = "error"
			}
			opt := metric.WithAttributes(
				attribute.String("server", server),
				attribute.String("tool", known.label(params.Name, status == "ok")),
				attribute.String("status", status),
			)
			ctx = context.WithoutCancel(ctx)
			m.calls.Add(ctx, 1, opt)
			m.duration.Record(ctx, time.Since(start).Seconds(), opt)
			return res, err
		}
	}
}
