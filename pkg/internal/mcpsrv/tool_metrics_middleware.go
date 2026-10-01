package mcpsrv

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"go.opentelemetry.io/otel/trace"
)

// ToolMetricsRecorder は tools/call 1 回ごとのイベントを受け取る。
// 実装は *toolmetrics.Recorder。
type ToolMetricsRecorder interface {
	Record(e toolmetrics.Event)
}

// NewToolMetricsMiddleware はサーバー serverName（サービス serviceCode）への
// tools/call ごとに toolmetrics.Event を作り、rec に渡す。認可で拒否された呼び出しも
// 数えるため、受信ミドルウェアの最も外側に置くこと。userHeader が空でなければ、
// その HTTP ヘッダーの値を Event.User に記録する（authz がユーザー ID を読むのと
// 同じヘッダー）。tools/call 以外のメソッドはそのまま通す。
func NewToolMetricsMiddleware(
	serverName, serviceCode, userHeader string,
	rec ToolMetricsRecorder,
) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != authzMethodToolsCall {
				return next(ctx, method, req)
			}
			start := time.Now()
			res, err := next(ctx, method, req)

			e := toolmetrics.Event{
				ID:         uuid.NewString(),
				Timestamp:  start.UTC(),
				Server:     serverName,
				Service:    serviceCode,
				DurationMs: time.Since(start).Milliseconds(),
			}
			if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
				e.Tool = params.Name
			}
			if extra := req.GetExtra(); extra != nil && userHeader != "" {
				e.User = extra.Header.Get(userHeader)
			}
			if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
				e.TraceID = sc.TraceID().String()
			}
			setToolMetricsStatus(&e, res, err)
			rec.Record(e)
			return res, err
		}
	}
}

func setToolMetricsStatus(e *toolmetrics.Event, res mcp.Result, err error) {
	if err != nil {
		e.Status = toolmetrics.StatusError
		var rpcErr *jsonrpc.Error
		if errors.As(err, &rpcErr) {
			e.ErrorCode = rpcErr.Code
			e.ErrorMessage = truncateUTF8(rpcErr.Message, toolmetrics.MaxErrorMessageLength)
		} else {
			e.ErrorMessage = truncateUTF8(err.Error(), toolmetrics.MaxErrorMessageLength)
		}
		return
	}
	if result, ok := res.(*mcp.CallToolResult); ok && result.IsError {
		e.Status = toolmetrics.StatusToolError
		e.ErrorMessage = truncateUTF8(callToolResultText(result), toolmetrics.MaxErrorMessageLength)
		return
	}
	e.Status = toolmetrics.StatusSuccess
}

// callToolResultText は r のテキストコンテンツを連結する。isError の結果では、
// ツールは人が読めるエラーメッセージをここに入れる。
func callToolResultText(r *mcp.CallToolResult) string {
	var parts []string
	for _, c := range r.Content {
		if t, ok := c.(*mcp.TextContent); ok && t.Text != "" {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// truncateUTF8 は s を、文字の途中で切らずに最大 n バイトに切り詰める。
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
