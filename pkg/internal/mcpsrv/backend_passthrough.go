package mcpsrv

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// normalizeCacheable は SEP-2549 のキャッシュ項目に既定値を入れる。cacheScope は
// ワイヤ上必須の enum（"public" / "private"）で、SDK の tools/list ハンドラは空なら
// "public" に正規化する。パススルーはそのハンドラを通らないため、同じ正規化を
// ここで行う。空のまま送ると enum を厳密に検証するクライアント（Postman 等）が
// レスポンス全体を捨て、ツールが 1 つも表示されない。
func normalizeCacheable(c *mcp.Cacheable) {
	if c.CacheScope == "" {
		c.CacheScope = "public"
	}
}

// backendPassthrough は newBackendPassthroughMiddleware の転送先。
// MCPBackendClient（MCP バックエンド）または A2ABackendClient（A2A エージェント）。
type backendPassthrough interface {
	ListTools(ctx context.Context, params *mcp.ListToolsParams) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (*mcp.CallToolResult, error)
}

// newBackendPassthroughMiddleware は MCP / A2A バックエンドサーバー向けに
// tools/list と tools/call をゲートウェイのツールレジストリを介さず
// バックエンドへ毎回転送するミドルウェアを返す。
//
// authz ミドルウェアより先に AddReceivingMiddleware すること。先に追加した
// ミドルウェアが内側になるため、authz が外側で tools/call を許可判定し、
// tools/list の結果（= バックエンドからの live な一覧）をフィルタできる。
func newBackendPassthroughMiddleware(bc backendPassthrough) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				// params は missingParamsOK のため nil がありうる。
				params, _ := req.GetParams().(*mcp.ListToolsParams)
				res, err := bc.ListTools(ctx, params)
				if err != nil {
					return nil, err
				}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok {
					return nil, &jsonrpc.Error{
						Code:    jsonrpc.CodeInvalidParams,
						Message: "invalid tools/call params",
					}
				}
				return bc.CallTool(ctx, params.Name, params.Arguments)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
