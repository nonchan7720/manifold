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
// route が一致しても、サービス自身が同名のツールを持つ場合はサービスが優先される
// （newServiceAgentsMiddleware 参照）。
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
//   - tools/call: ツール名の最初の "__" より前が追加したエージェント名なら、
//     message/send へ転送する。ただしサービス自身が同名のツールを持つ場合は
//     サービスのツールが優先され、next へ渡す（エージェント側はリネームで解消できる）。
//     それ以外も next（サービス自身のツール）。
//   - tools/list: next の結果（サービス自身のツール）の後ろにエージェントのツールを
//     足す。サービスのツールと名前が衝突するエージェントのツールは、警告ログを出して
//     一覧から外す（サービスのツールは絞らない）。ページングされている場合は
//     最後（または唯一）のページにだけ足す。
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
				agentTools := sa.listTools(ctx)
				if len(agentTools) > 0 {
					serviceNames := sa.serviceToolNames(ctx, next, req, list)
					agentTools = sa.dropCollisions(ctx, serviceNames, agentTools)
				}
				// SDK 側が保持しているスライスへ書き込まないよう、複製してから足す。
				list.Tools = append(slices.Clone(list.Tools), agentTools...)
				normalizeCacheable(&list.Cacheable)
				return list, nil
			case authzMethodToolsCall:
				// 想定外の params は next（サービス側）に任せてエラーにさせる。
				if params, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok {
					if client, ok := sa.route(params.Name); ok &&
						!sa.serviceHasTool(ctx, next, req, params.Name) {
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

// maxServiceToolPages はサービスのツール一覧を辿るページ数の上限。カーソルが
// 進まないバックエンドで無限ループにならないための保険。
const maxServiceToolPages = 100

// walkServiceTools はサービス自身の tools/list を next 経由で cursor から最後の
// ページまで辿り、各ツールを visit に渡す。visit が true を返したら打ち切る。
// リクエストは受信中の req から作り直す（セッションと Extra は引き継ぐ）。
func walkServiceTools(
	ctx context.Context,
	next mcp.MethodHandler,
	req mcp.Request,
	cursor string,
	visit func(*mcp.Tool) bool,
) error {
	session, _ := req.GetSession().(*mcp.ServerSession)
	for range maxServiceToolPages {
		listReq := &mcp.ServerRequest[*mcp.ListToolsParams]{
			Session: session,
			Params:  &mcp.ListToolsParams{Cursor: cursor},
			Extra:   req.GetExtra(),
		}
		res, err := next(ctx, authzMethodToolsList, listReq)
		if err != nil {
			return err
		}
		list, ok := res.(*mcp.ListToolsResult)
		if !ok || list == nil {
			return nil
		}
		if slices.ContainsFunc(list.Tools, visit) {
			return nil
		}
		if list.NextCursor == "" || list.NextCursor == cursor {
			return nil
		}
		cursor = list.NextCursor
	}
	return nil
}

// serviceHasTool はサービス自身が name のツールを持つかを返す。tools/call が
// エージェントへ振り分けられる名前のときだけ呼ぶ。MCP バックエンドではエージェント
// 呼び出し 1 回につき tools/list を 1 往復（ページングがあればその分）余計に
// 行うが、衝突時にサービスを優先するために許容する。OpenAPI モードでは SDK の
// プロセス内ハンドラを呼ぶだけ。tools/list に失敗した場合（バックエンド停止など）は
// 警告ログを出して false を返し、エージェント呼び出しを妨げない。
func (sa *serviceAgents) serviceHasTool(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request, name string,
) bool {
	found := false
	err := walkServiceTools(ctx, next, req, "", func(tool *mcp.Tool) bool {
		found = tool.Name == name
		return found
	})
	if err != nil {
		slog.WarnContext(
			ctx,
			"service tools/list failed while checking for a tool name collision; "+
				"routing to the agent",
			slog.String("server", sa.server),
			slog.String("tool", name),
			slog.Any("error", err),
		)
		return false
	}
	return found
}

// serviceToolNames は tools/list の最後のページ（first）を含む、サービス自身の
// ツール名の集合を返す。リクエストがカーソル付き（= 前のページがある）のときは
// 先頭から辿り直す。取得に失敗したら警告ログを出し、分かった範囲の名前を返す。
func (sa *serviceAgents) serviceToolNames(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request, first *mcp.ListToolsResult,
) map[string]struct{} {
	names := make(map[string]struct{}, len(first.Tools))
	for _, tool := range first.Tools {
		names[tool.Name] = struct{}{}
	}
	if params, _ := req.GetParams().(*mcp.ListToolsParams); params != nil && params.Cursor != "" {
		err := walkServiceTools(ctx, next, req, "", func(tool *mcp.Tool) bool {
			names[tool.Name] = struct{}{}
			return false
		})
		if err != nil {
			slog.WarnContext(
				ctx,
				"service tools/list failed while checking for tool name collisions",
				slog.String("server", sa.server),
				slog.Any("error", err),
			)
		}
	}
	return names
}

// warnCollision はエージェントのツールがサービスのツールと衝突したことを警告する。
func (sa *serviceAgents) warnCollision(ctx context.Context, tool string) {
	agent, _, _ := strings.Cut(tool, config.AgentToolSeparator)
	slog.WarnContext(ctx,
		"agent tool name collides with a service tool; the service tool wins, rename the agent",
		slog.String("server", sa.server), slog.String("agent", agent), slog.String("tool", tool))
}

// dropCollisions は serviceNames（サービス自身のツール名）と同名のエージェントの
// ツールを、警告ログを出して取り除く。サービスのツールは絞らない。
func (sa *serviceAgents) dropCollisions(
	ctx context.Context, serviceNames map[string]struct{}, agentTools []*mcp.Tool,
) []*mcp.Tool {
	kept := make([]*mcp.Tool, 0, len(agentTools))
	for _, tool := range agentTools {
		if _, collides := serviceNames[tool.Name]; collides {
			sa.warnCollision(ctx, tool.Name)
			continue
		}
		kept = append(kept, tool)
	}
	return kept
}

// dropCollidingInfos は dropCollisions の /mcp/list 用。serviceInfos と同名の
// agentInfos を、警告ログを出して取り除く。
func (sa *serviceAgents) dropCollidingInfos(
	ctx context.Context, serviceInfos, agentInfos []ToolInfo,
) []ToolInfo {
	serviceNames := make(map[string]struct{}, len(serviceInfos))
	for _, info := range serviceInfos {
		serviceNames[info.Name] = struct{}{}
	}
	kept := make([]ToolInfo, 0, len(agentInfos))
	for _, info := range agentInfos {
		if _, collides := serviceNames[info.Name]; collides {
			sa.warnCollision(ctx, info.Name)
			continue
		}
		kept = append(kept, info)
	}
	return kept
}
