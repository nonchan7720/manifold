package mcpsrv

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// prompts/* は今のところ authz の対象外（newBackendPromptsMiddleware が
	// そのまま転送する）。対象にする場合は NewAuthzMiddleware でこれらを判定する。
	methodPromptsList = "prompts/list"
	methodPromptsGet  = "prompts/get"
)

// unknownPromptError は SDK の組み込みハンドラ（Server.getPrompt）と同じ、
// 存在しない prompt に対するエラーを返す。
func unknownPromptError(name string) error {
	return &jsonrpc.Error{
		Code:    jsonrpc.CodeInvalidParams,
		Message: fmt.Sprintf("unknown prompt %q", name),
	}
}

// backendPrompts は newBackendPromptsMiddleware の転送先（MCPBackendClient）。
type backendPrompts interface {
	ListPrompts(ctx context.Context, params *mcp.ListPromptsParams) (*mcp.ListPromptsResult, error)
	GetPrompt(ctx context.Context, params *mcp.GetPromptParams) (*mcp.GetPromptResult, error)
}

// newBackendPromptsMiddleware は MCP バックエンドサーバー向けに prompts/list・
// prompts/get をバックエンドへ毎回転送するミドルウェアを返す。エンドポイント
// （/mcp/<name>）がバックエンドごとに分かれているので、prompt 名の書き換えは不要。
//
// tools/*・resources/* は扱わないので newBackendPassthroughMiddleware・
// newBackendResourcesMiddleware と並べて追加する。prompts は今のところツールの
// authz・キャッシュ・フィルタの対象外で、エンドポイントの認証のみが掛かる。
func newBackendPromptsMiddleware(bc backendPrompts) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case methodPromptsList:
				// params は省略可能なため nil がありうる。
				params, _ := req.GetParams().(*mcp.ListPromptsParams)
				res, err := bc.ListPrompts(ctx, withoutProtocolMeta(params,
					func(p *mcp.ListPromptsParams) *mcp.Meta { return &p.Meta }))
				if err != nil {
					return nil, err
				}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case methodPromptsGet:
				params, _ := req.GetParams().(*mcp.GetPromptParams)
				if params == nil {
					return nil, unknownPromptError("")
				}
				res, err := bc.GetPrompt(ctx, withoutProtocolMeta(params,
					func(p *mcp.GetPromptParams) *mcp.Meta { return &p.Meta }))
				if err != nil {
					return nil, err
				}
				// tools/call と同じく、SDK の組み込みハンドラが付ける resultType
				// （2026-07-28 以降で必須）をここで補う。
				return completeRelayedResult(req, res)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
