package mcpsrv

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// errToolNotInScope は、リクエストのツールスコープ外のサーバーへの tools/call に
// 返すエラー。スコープはクライアント自身が指定したものなので、authz の拒否と
// 違って理由を伏せる必要はない。
var errToolNotInScope = &jsonrpc.Error{
	Code:    jsonrpc.CodeInvalidParams,
	Message: "tool not enabled in this request's tool scope",
}

// toolScopeList は受信ヘッダー name の値をカンマ区切りの一覧として返す。
// ok はヘッダーが付いているかどうか。付いていて値が空なら ok = true で空の
// 一覧になり、そのリクエストでは何も有効にしない指定として扱う。
func toolScopeList(header http.Header, name string) (list []string, ok bool) {
	values, ok := header[http.CanonicalHeaderKey(name)]
	if !ok {
		return nil, false
	}
	for _, v := range values {
		for item := range strings.SplitSeq(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				list = append(list, item)
			}
		}
	}
	return list, true
}

// toolScopeAllows は header のスコープ指定のもとで、サービス serviceCode に属する
// サーバー serverName が有効かどうかを返す。scoped はスコープ指定のヘッダーが
// 1 つでも付いているかどうか。services / servers の両方が付いていれば、両方に
// 含まれるサーバーだけが有効になる。
func toolScopeAllows(
	header http.Header, headers config.ToolScopeHeaders, serverName, serviceCode string,
) (allowed, scoped bool) {
	allowed = true
	if services, ok := toolScopeList(header, headers.Services); ok {
		scoped = true
		allowed = allowed && slices.Contains(services, serviceCode)
	}
	if servers, ok := toolScopeList(header, headers.Servers); ok {
		scoped = true
		allowed = allowed && slices.Contains(servers, serverName)
	}
	return allowed, scoped
}

// markToolScoped は tools/list の結果がリクエストのスコープ指定に依存することを
// クライアントに伝えるため、キャッシュを本人のクライアントに限り（private）、
// 即座に古くなる（TTL 0）ものとして広告する。ターンごとにスコープを切り替える
// クライアントが前のターンの一覧を使い回さないようにするため。
func markToolScoped(res *mcp.ListToolsResult) {
	res.TTLMs = 0
	res.CacheScope = "private"
}

// NewToolScopeMiddleware は、サービス serviceCode に属するサーバー serverName に
// ついて、リクエストごとのツールスコープ（config.ToolScopeHeaders）を強制する
// mcp.Middleware を返す。
//
// スコープ外のサーバーは tools/list で空の一覧を返し、tools/call を拒否する。
// どちらも next を呼ばないため、authz（OPA）への問い合わせやバックエンドへの
// 転送も起きない。スコープ指定のヘッダーが無いリクエストと、HTTP 以外の
// トランスポート（Extra が nil）はそのまま next へ渡す。
//
// authz ミドルウェアより外側に置くこと（middlewareFn が返す一覧では先頭）。
// スコープはあくまで authz が許可したツールをさらに絞り込むもので、許可を
// 広げることはない。
func NewToolScopeMiddleware(
	serverName, serviceCode string, headers config.ToolScopeHeaders,
) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != authzMethodToolsCall && method != authzMethodToolsList {
				return next(ctx, method, req)
			}
			extra := req.GetExtra()
			if extra == nil {
				return next(ctx, method, req)
			}
			allowed, scoped := toolScopeAllows(extra.Header, headers, serverName, serviceCode)
			if !scoped {
				return next(ctx, method, req)
			}
			if !allowed {
				slog.DebugContext(ctx, "tool scope: server out of scope",
					slog.String("server", serverName), slog.String("service", serviceCode),
					slog.String("method", method))
				if method == authzMethodToolsCall {
					return nil, errToolNotInScope
				}
				res := &mcp.ListToolsResult{Tools: []*mcp.Tool{}}
				markToolScoped(res)
				return res, nil
			}

			res, err := next(ctx, method, req)
			if err != nil {
				return nil, err
			}
			if list, ok := res.(*mcp.ListToolsResult); ok {
				markToolScoped(list)
			}
			return res, nil
		}
	}
}
