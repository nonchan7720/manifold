package mcpsrv

import (
	"context"
	"encoding/json"
	"maps"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// MCP Apps（SEP-1865, io.modelcontextprotocol/ui 拡張）への対応。
//
// MCP Apps では、サーバーがツールの _meta.ui.resourceUri に ui:// リソースを
// 指定し、ホスト（MCP クライアント）がそのリソース（text/html;profile=mcp-app）を
// resources/read で取得して iframe に描画する。ホストは capabilities.extensions
// ["io.modelcontextprotocol/ui"] で対応を申告し、サーバーはそれを見て UI 付きの
// ツールを返すかを決める。
//
// ゲートウェイは MCP バックエンドに対して次を担う。
//
//   - 呼び出し元が UI 対応かを判定する（mcpAppsUISettings）。リクエストの申告を
//     優先し、申告が無ければ mcpServers.<name>.apps に従う。ゲートウェイは HTTP を
//     Stateless で配信するため、2026-07-28 より前のプロトコルのホストが initialize で
//     申告した内容は以降のリクエストでは分からない。
//   - UI 対応の呼び出し元には tools/list の _meta.ui をそのまま返す。UI 非対応の
//     呼び出し元には _meta.ui を取り除き、UI からしか呼ばないツール
//     （visibility が "model" を含まない）を一覧から外す（newMCPAppsMiddleware）。
//   - resources/list・resources/templates/list・resources/read をバックエンドへ
//     転送する（newBackendResourcesMiddleware）。
//   - バックエンドへの initialize で UI 拡張を広告する。http バックエンドは
//     呼び出しごとにセッションを張るので UI 対応の呼び出し元のときだけ広告し、
//     stdio バックエンドは全呼び出し元で1セッションを共有するので常に広告する
//     （UI 非対応の呼び出し元へは上記のとおりゲートウェイ側で UI を取り除く）。

const (
	// mcpAppsExtension は MCP Apps の拡張 ID。
	mcpAppsExtension = "io.modelcontextprotocol/ui"
	// mcpAppsMIMEType は MCP Apps の UI リソースの MIME タイプ。
	mcpAppsMIMEType = "text/html;profile=mcp-app"

	// mcpAppsMetaKey はツールの _meta で UI を指定するキー。
	mcpAppsMetaKey = "ui"
	// mcpAppsLegacyResourceURIMetaKey は旧ドラフトの _meta["ui/resourceUri"]。
	mcpAppsLegacyResourceURIMetaKey = "ui/resourceUri"
	// mcpAppsVisibilityModel は _meta.ui.visibility でモデルから見えることを表す値。
	mcpAppsVisibilityModel = "model"
)

// defaultMCPAppsExtensionSettings は呼び出し元の申告を使えない場合（stdio の共有
// セッション、mcpServers.<name>.apps による判定）にバックエンドへ広告する UI 拡張の設定。
func defaultMCPAppsExtensionSettings() map[string]any {
	return map[string]any{"mimeTypes": []any{mcpAppsMIMEType}}
}

type callerUIExtensionKey struct{}

// withCallerUIExtension は UI 対応の呼び出し元の UI 拡張の設定を ctx に載せる。
// MCPBackendClient.connect が http バックエンドへの initialize で広告するために使う。
func withCallerUIExtension(ctx context.Context, settings map[string]any) context.Context {
	return context.WithValue(ctx, callerUIExtensionKey{}, settings)
}

// callerUIExtensionFromContext は withCallerUIExtension が載せた設定を返す。
func callerUIExtensionFromContext(ctx context.Context) (map[string]any, bool) {
	settings, ok := ctx.Value(callerUIExtensionKey{}).(map[string]any)
	return settings, ok
}

// callerUIExtension は req の呼び出し元クライアントが申告した UI 拡張の設定を返す。
// 2026-07-28 以降のプロトコルではリクエストごとの _meta、それ以前は initialize の
// capabilities から読む（mcp.ServerRequest.ClientCapabilities）。Stateless 配信では
// 後者は initialize リクエスト以外では空になる。
func callerUIExtension(req mcp.Request) (map[string]any, bool) {
	if req == nil {
		return nil, false
	}
	r, ok := req.(interface {
		ClientCapabilities() *mcp.ClientCapabilities
	})
	if !ok {
		return nil, false
	}
	caps := r.ClientCapabilities()
	if caps == nil {
		return nil, false
	}
	raw, ok := caps.Extensions[mcpAppsExtension]
	if !ok {
		return nil, false
	}
	switch v := raw.(type) {
	case map[string]any:
		return maps.Clone(v), true
	case nil:
		return map[string]any{}, true
	default:
		// 型付きの値（プロセス内で組み立てられた場合）は JSON を経由して map にする。
		b, err := json.Marshal(v)
		if err != nil {
			return map[string]any{}, true
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil || m == nil {
			return map[string]any{}, true
		}
		return m, true
	}
}

// mcpAppsUISettings は呼び出し元を UI 対応とみなすかと、そのときバックエンドへ
// 広告する UI 拡張の設定を返す。リクエストの申告を優先し、申告が無ければ
// assumeSupport（mcpServers.<name>.apps）に従う。
func mcpAppsUISettings(req mcp.Request, assumeSupport bool) (map[string]any, bool) {
	if settings, ok := callerUIExtension(req); ok {
		return settings, true
	}
	if assumeSupport {
		return defaultMCPAppsExtensionSettings(), true
	}
	return nil, false
}

// newMCPAppsMiddleware は MCP バックエンドサーバー向けに、呼び出し元の UI 対応を
// 判定して ctx に載せ（バックエンドへの広告に使う）、UI 非対応の呼び出し元への
// tools/list から UI を取り除くミドルウェアを返す。パススルー
// （newBackendPassthroughMiddleware・newBackendResourcesMiddleware）の外側に置く。
func newMCPAppsMiddleware(server *config.Server) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			settings, ui := mcpAppsUISettings(req, server.Apps)
			if ui {
				return next(withCallerUIExtension(ctx, settings), method, req)
			}
			res, err := next(ctx, method, req)
			if err != nil || method != authzMethodToolsList {
				return res, err
			}
			if result, ok := res.(*mcp.ListToolsResult); ok {
				copied := *result
				copied.Tools = stripMCPAppsTools(result.Tools)
				return &copied, nil
			}
			return res, nil
		}
	}
}

// stripMCPAppsTools は UI 非対応の呼び出し元向けに、UI からしか呼ばないツールを
// 外し、残りのツールの _meta から UI の指定を取り除く。tools は変更せず複製する。
func stripMCPAppsTools(tools []*mcp.Tool) []*mcp.Tool {
	out := make([]*mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		_, hasUI := tool.Meta[mcpAppsMetaKey]
		_, hasLegacy := tool.Meta[mcpAppsLegacyResourceURIMetaKey]
		if !hasUI && !hasLegacy {
			out = append(out, tool)
			continue
		}
		if !visibleToModel(tool.Meta[mcpAppsMetaKey]) {
			continue
		}
		copied := *tool
		copied.Meta = maps.Clone(tool.Meta)
		delete(copied.Meta, mcpAppsMetaKey)
		delete(copied.Meta, mcpAppsLegacyResourceURIMetaKey)
		if len(copied.Meta) == 0 {
			copied.Meta = nil
		}
		out = append(out, &copied)
	}
	return out
}

// visibleToModel は _meta.ui の visibility がモデルを含むかを返す。visibility が
// 無ければ既定（["model", "app"]）としてモデルから見える。
func visibleToModel(ui any) bool {
	m, ok := ui.(map[string]any)
	if !ok {
		return true
	}
	raw, ok := m["visibility"]
	if !ok {
		return true
	}
	values, ok := raw.([]any)
	if !ok {
		return true
	}
	return slices.Contains(values, any(mcpAppsVisibilityModel))
}

// backendResources は newBackendResourcesMiddleware の転送先（MCPBackendClient）。
type backendResources interface {
	ListResources(
		ctx context.Context,
		params *mcp.ListResourcesParams,
	) (*mcp.ListResourcesResult, error)
	ListResourceTemplates(
		ctx context.Context,
		params *mcp.ListResourceTemplatesParams,
	) (*mcp.ListResourceTemplatesResult, error)
	ReadResource(
		ctx context.Context,
		params *mcp.ReadResourceParams,
	) (*mcp.ReadResourceResult, error)
}

// newBackendResourcesMiddleware は MCP バックエンドサーバー向けに resources/list・
// resources/templates/list・resources/read をバックエンドへ毎回転送する
// ミドルウェアを返す。MCP Apps の ui:// リソースをホストが取得できるようにするため。
// エンドポイント（/mcp/<name>）がバックエンドごとに分かれているので、URI の
// 書き換えは不要。
//
// tools/* は扱わないので newBackendPassthroughMiddleware と並べて追加する。
// リソースは今のところツールの authz・キャッシュ・フィルタの対象外で、
// エンドポイントの認証のみが掛かる。authz を掛ける場合は、このミドルウェアより
// 外側にある authz ミドルウェアで authzMethodResourcesRead を判定すればよい。
func newBackendResourcesMiddleware(bc backendResources) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodResourcesList:
				params, _ := req.GetParams().(*mcp.ListResourcesParams)
				res, err := bc.ListResources(ctx, withoutProtocolMeta(params,
					func(p *mcp.ListResourcesParams) *mcp.Meta { return &p.Meta }))
				if err != nil {
					return nil, err
				}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case authzMethodResourcesTemplatesList:
				params, _ := req.GetParams().(*mcp.ListResourceTemplatesParams)
				res, err := bc.ListResourceTemplates(ctx, withoutProtocolMeta(params,
					func(p *mcp.ListResourceTemplatesParams) *mcp.Meta { return &p.Meta }))
				if err != nil {
					return nil, err
				}
				normalizeCacheable(&res.Cacheable)
				return res, nil
			case authzMethodResourcesRead:
				params, _ := req.GetParams().(*mcp.ReadResourceParams)
				if params == nil {
					return nil, mcp.ResourceNotFoundError("")
				}
				res, err := bc.ReadResource(ctx, withoutProtocolMeta(params,
					func(p *mcp.ReadResourceParams) *mcp.Meta { return &p.Meta }))
				if err != nil {
					return nil, err
				}
				normalizeCacheable(&res.Cacheable)
				// tools/call と同じく、SDK の組み込みハンドラが付ける resultType
				// （2026-07-28 以降で必須）をここで補う。
				return completeRelayedResult(req, res)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
