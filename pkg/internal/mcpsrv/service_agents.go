package mcpsrv

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
)

// serviceAgents は 1 つのサービス（mcpServers.<name>）にぶら下げた A2A エージェント
// （mcpServers.<name>.agents）のクライアント群。エージェント名の昇順に保持し、
// tools/list の並びを決定的にする。各クライアントのスキルは <agent>__<skill> の
// ツール名で公開される（config.AgentToolName 参照）。
type serviceAgents struct {
	server  string
	names   []string
	clients map[string]*A2ABackendClient
}

// newServiceAgents は agents のエージェントごとに A2ABackendClient を組み立てる。
// ログ・エラーに使うクライアント名は <サービス名>/<エージェント名>。
func newServiceAgents(
	server string, agents config.Agents, mediaService storage.MediaService,
) *serviceAgents {
	sa := &serviceAgents{
		server:  server,
		names:   slices.Sorted(maps.Keys(agents)),
		clients: make(map[string]*A2ABackendClient, len(agents)),
	}
	for _, agentName := range sa.names {
		sa.clients[agentName] = NewA2ABackendClient(
			server+"/"+agentName,
			agents[agentName].Server(),
			mediaService,
			withToolPrefix(config.AgentToolName(agentName, "")),
		)
	}
	return sa
}

// ensureCards は各エージェントの Agent Card を取得しておく。失敗しても起動は
// 止めず警告だけ出す（最初のリクエストで取り直す）。トップレベルの agents と同じ。
func (sa *serviceAgents) ensureCards(ctx context.Context) {
	for _, agentName := range sa.names {
		if _, err := sa.clients[agentName].EnsureCard(ctx); err != nil {
			slog.WarnContext(ctx, "a2a agent card fetch failed; retrying on first request",
				slog.String("server", sa.server), slog.String("agent", agentName),
				slog.Any("error", err))
		}
	}
}

// listTools は全エージェントのスキルツールをエージェント名順に返す。Agent Card を
// 取得できないエージェントは ERROR ログを出して読み飛ばし、サービス自身のツールや
// 他のエージェントのツールまで tools/list ごと失敗させない。
func (sa *serviceAgents) listTools(ctx context.Context) []*mcp.Tool {
	var tools []*mcp.Tool
	for _, agentName := range sa.names {
		res, err := sa.clients[agentName].ListTools(ctx, nil)
		if err != nil {
			slog.ErrorContext(ctx, "service agent skipped from tools/list: agent card unavailable",
				slog.String("server", sa.server), slog.String("agent", agentName),
				slog.Any("error", err))
			continue
		}
		tools = append(tools, res.Tools...)
	}
	return tools
}

// listToolInfos は /mcp/list 用に listTools と同じ方針でツール情報を返す。
func (sa *serviceAgents) listToolInfos(ctx context.Context) []ToolInfo {
	var infos []ToolInfo
	for _, agentName := range sa.names {
		agentInfos, err := sa.clients[agentName].ListToolInfos(ctx)
		if err != nil {
			slog.ErrorContext(
				ctx,
				"service agent skipped from tool catalog: agent card unavailable",
				slog.String("server", sa.server),
				slog.String("agent", agentName),
				slog.Any("error", err),
			)
			continue
		}
		infos = append(infos, agentInfos...)
	}
	return infos
}

// route は tools/call のツール名から担当のエージェントクライアントを返す。
// エージェント名は "__" を含められないため、最初の "__" より前がエージェント名に
// なり、一致するクライアントは高々 1 つ。サービス自身のツールなら false。
func (sa *serviceAgents) route(name string) (*A2ABackendClient, bool) {
	agentName, _, found := strings.Cut(name, config.AgentToolSeparator)
	if !found {
		return nil, false
	}
	client, ok := sa.clients[agentName]
	return client, ok
}

// close は全エージェントクライアントを閉じる。
func (sa *serviceAgents) close() {
	for _, client := range sa.clients {
		client.Close()
	}
}

// newServiceAgentsMiddleware はサービスにぶら下げたエージェントを、サービス自身の
// ツールと並べて公開するミドルウェアを返す。
//
//   - tools/call: ツール名が <agent>__ で始まりそのエージェントが存在すれば、
//     message/send へ転送する。それ以外は next（サービス自身のツール）。
//   - tools/list: next の結果（サービス自身のツール）の後ろにエージェントのツールを
//     足す。ページングされている場合は最後（または唯一）のページにだけ足す。
//
// バックエンドのパススルー（newBackendPassthroughMiddleware）より後、authz より
// 先に AddReceivingMiddleware すること。パススルーの外側に置くことで、OpenAPI
// モードでは SDK 自身の tools/list ハンドラの結果にも足せる。authz が更に外側に
// あるため、エージェントのツールも server=<サービス名>, tool=<agent>__<skill> で
// 許可判定とフィルタの対象になる。
func newServiceAgentsMiddleware(sa *serviceAgents) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsList:
				res, err := next(ctx, method, req)
				if err != nil {
					return nil, err
				}
				list, ok := res.(*mcp.ListToolsResult)
				if !ok || list == nil || list.NextCursor != "" {
					return res, nil
				}
				// SDK 側が保持しているスライスへ書き込まないよう、複製してから足す。
				list.Tools = append(slices.Clone(list.Tools), sa.listTools(ctx)...)
				normalizeCacheable(&list.Cacheable)
				return list, nil
			case authzMethodToolsCall:
				// 想定外の params は next（サービス側）に任せてエラーにさせる。
				if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
					if client, ok := sa.route(params.Name); ok {
						return client.CallTool(ctx, params.Name, params.Arguments)
					}
				}
				return next(ctx, method, req)
			default:
				return next(ctx, method, req)
			}
		}
	}
}
