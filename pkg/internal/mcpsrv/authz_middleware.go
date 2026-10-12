package mcpsrv

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/services/authz"
)

const (
	authzMethodToolsCall = "tools/call"
	authzMethodToolsList = "tools/list"

	// prompts にはツールの利用に関わる指示が含まれうるため、ツールと同じ判定に
	// 掛ける。OPA への入力もツールと同じで、prompts/get は decisionPath.call に
	// prompt 名を tool として、prompts/list は decisionPath.list に prompt を
	// tools の要素として渡す。
	authzMethodPromptsList = methodPromptsList
	authzMethodPromptsGet  = methodPromptsGet

	// authzPromptPrefix は authz の判定に渡す prompt 名の接頭辞。同じ名前の
	// ツールと prompt がポリシー上で区別できるようにする。":" は MCP が
	// ツール名に推奨する文字（英数字・"_"・"-"・"."）に含まれないため、
	// ツール名と衝突しにくい。
	authzPromptPrefix = "prompt:"

	// resources/* は今のところ authz の対象外（MCP バックエンドの
	// newBackendResourcesMiddleware がそのまま転送する）。対象にする場合は
	// NewAuthzMiddleware でこれらを判定する。
	authzMethodResourcesList          = "resources/list"
	authzMethodResourcesTemplatesList = "resources/templates/list"
	authzMethodResourcesRead          = "resources/read"
)

// errToolNotAllowedByPolicy is the only detail returned to clients for any
// authz denial (missing identity, policy deny, or a Decider failure) — the
// reason is logged server-side, never echoed back.
var errToolNotAllowedByPolicy = &jsonrpc.Error{
	Code:    jsonrpc.CodeInternalError,
	Message: "tool not allowed by policy",
}

// authzPrincipal resolves the calling Principal from req's HTTP headers,
// including the config.AuthzInput.FromHeaders fields. A nil Extra (e.g.
// non-HTTP transports) is treated the same as a missing identity header.
func authzPrincipal(
	req mcp.Request,
	headers config.AuthzHeaders,
	fromHeaders map[string]config.AuthzInputHeaderField,
) (authz.Principal, error) {
	extra := req.GetExtra()
	if extra == nil {
		return authz.Principal{}, authz.ErrMissingIdentity
	}
	return authz.PrincipalFromHeader(extra.Header, headers, fromHeaders)
}

// authzBypassRequested reports whether req's HTTP headers carry
// headers.Bypass per authz.BypassRequested. A nil Extra (non-HTTP
// transports) never bypasses.
func authzBypassRequested(req mcp.Request, headers config.AuthzHeaders) bool {
	extra := req.GetExtra()
	if extra == nil {
		return false
	}
	return authz.BypassRequested(extra.Header, headers)
}

// authzHandleToolCall enforces d.Allow for a tools/call request, forwarding
// to next only when allowed. Any non-allow outcome (deny, Decider error, or
// an unexpected params type) maps to the same fixed JSON-RPC error.
func authzHandleToolCall(
	ctx context.Context,
	serverName, serviceCode string,
	d authz.Decider,
	p authz.Principal,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
) (mcp.Result, error) {
	var name string
	switch params := req.GetParams().(type) {
	case *mcp.CallToolParamsRaw:
		name = params.Name
	case *mcp.GetPromptParams:
		name = authzPromptPrefix + params.Name
	default:
		slog.ErrorContext(ctx, "authz: unexpected params type on "+method,
			slog.String("server", serverName), slog.String("reason", "unexpected_params"))
		return nil, errToolNotAllowedByPolicy
	}
	tool := authz.ToolRef{Server: serverName, Service: serviceCode, Name: name}

	allowed, err := d.Allow(ctx, p, tool)
	if err != nil {
		slog.ErrorContext(ctx, "authz: decider error on "+method,
			slog.String("server", serverName), slog.String("tool", tool.Name),
			slog.Any("error", err))
		return nil, errToolNotAllowedByPolicy
	}

	decision := "deny"
	if allowed {
		decision = "allow"
	}
	slog.InfoContext(ctx, "authz decision",
		slog.String("user", p.UserID), slog.Any("groups", p.Groups),
		slog.String("server", serverName), slog.String("service", serviceCode),
		slog.String("method", method),
		slog.String("tool", tool.Name), slog.String("decision", decision))

	if !allowed {
		return nil, errToolNotAllowedByPolicy
	}
	return next(ctx, method, req)
}

// authzHandleToolsList calls next to obtain the full tool list, then narrows
// it to the subset d.AllowedTools reports, preserving order. An empty list
// skips the Decider call entirely.
func authzHandleToolsList(
	ctx context.Context,
	serverName, serviceCode string,
	d authz.Decider,
	p authz.Principal,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
) (mcp.Result, error) {
	res, err := next(ctx, method, req)
	if err != nil {
		return nil, err
	}
	result, ok := res.(*mcp.ListToolsResult)
	if !ok || len(result.Tools) == 0 {
		return res, nil
	}

	refs := make([]authz.ToolRef, len(result.Tools))
	for i, tool := range result.Tools {
		refs[i] = authz.ToolRef{Server: serverName, Service: serviceCode, Name: tool.Name}
	}
	allowed, err := d.AllowedTools(ctx, p, refs)
	if err != nil {
		slog.ErrorContext(ctx, "authz: decider error on tools/list",
			slog.String("server", serverName), slog.Any("error", err))
		return nil, errToolNotAllowedByPolicy
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		allowedSet[t.Name] = struct{}{}
	}
	filtered := make([]*mcp.Tool, 0, len(allowed))
	for _, tool := range result.Tools {
		if _, ok := allowedSet[tool.Name]; ok {
			filtered = append(filtered, tool)
		}
	}
	result.Tools = filtered

	slog.InfoContext(ctx, "authz decision",
		slog.String("user", p.UserID), slog.Any("groups", p.Groups),
		slog.String("server", serverName), slog.String("service", serviceCode),
		slog.String("method", authzMethodToolsList),
		slog.Int("allowed", len(result.Tools)), slog.Int("total", len(refs)))
	return result, nil
}

// authzHandlePromptsList は authzHandleToolsList と同じく、next の prompts/list の
// 結果を d.AllowedTools が許可した prompt に絞る（prompt は名前に
// authzPromptPrefix を付け、ツールと同じ形で判定に渡す）。空の一覧では
// Decider を呼ばない。
func authzHandlePromptsList(
	ctx context.Context,
	serverName, serviceCode string,
	d authz.Decider,
	p authz.Principal,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
) (mcp.Result, error) {
	res, err := next(ctx, method, req)
	if err != nil {
		return nil, err
	}
	result, ok := res.(*mcp.ListPromptsResult)
	if !ok || len(result.Prompts) == 0 {
		return res, nil
	}

	refs := make([]authz.ToolRef, len(result.Prompts))
	for i, prompt := range result.Prompts {
		refs[i] = authz.ToolRef{
			Server: serverName, Service: serviceCode, Name: authzPromptPrefix + prompt.Name,
		}
	}
	allowed, err := d.AllowedTools(ctx, p, refs)
	if err != nil {
		slog.ErrorContext(ctx, "authz: decider error on prompts/list",
			slog.String("server", serverName), slog.Any("error", err))
		return nil, errToolNotAllowedByPolicy
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		allowedSet[t.Name] = struct{}{}
	}
	filtered := make([]*mcp.Prompt, 0, len(allowed))
	for _, prompt := range result.Prompts {
		if _, ok := allowedSet[authzPromptPrefix+prompt.Name]; ok {
			filtered = append(filtered, prompt)
		}
	}
	// 結果はパススルーの取得元やバックエンドと共有している可能性があるため複製する。
	copied := *result
	copied.Prompts = filtered

	slog.InfoContext(ctx, "authz decision",
		slog.String("user", p.UserID), slog.Any("groups", p.Groups),
		slog.String("server", serverName), slog.String("service", serviceCode),
		slog.String("method", authzMethodPromptsList),
		slog.Int("allowed", len(filtered)), slog.Int("total", len(refs)))
	return &copied, nil
}

// NewAuthzMiddleware builds a mcp.Middleware enforcing d as the PEP for
// server serverName, which belongs to service serviceCode
// (config.Server.ServiceCode). It fails closed: a missing/unresolvable
// identity denies without querying d, and any tools/call, tools/list,
// prompts/get or prompts/list Decider error also denies rather than falling
// through to next. Prompts are decided with the same tool decisions (see
// authzMethodPromptsList).
func NewAuthzMiddleware(
	serverName, serviceCode string,
	d authz.Decider,
	headers config.AuthzHeaders,
	fromHeaders map[string]config.AuthzInputHeaderField,
) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case authzMethodToolsCall, authzMethodToolsList,
				authzMethodPromptsGet, authzMethodPromptsList:
			default:
				return next(ctx, method, req)
			}

			if authzBypassRequested(req, headers) {
				slog.InfoContext(ctx, "authz decision",
					slog.String("server", serverName), slog.String("method", method),
					slog.String("decision", "bypass"))
				return next(ctx, method, req)
			}

			p, err := authzPrincipal(req, headers, fromHeaders)
			if err != nil {
				slog.InfoContext(ctx, "authz decision",
					slog.String("server", serverName), slog.String("method", method),
					slog.String("decision", "deny"),
					slog.String("reason", authz.DenyReason(err)), slog.Any("error", err))
				return nil, errToolNotAllowedByPolicy
			}

			switch method {
			case authzMethodToolsCall, authzMethodPromptsGet:
				return authzHandleToolCall(ctx, serverName, serviceCode, d, p, next, method, req)
			case authzMethodPromptsList:
				return authzHandlePromptsList(ctx, serverName, serviceCode, d, p, next, method, req)
			default:
				return authzHandleToolsList(ctx, serverName, serviceCode, d, p, next, method, req)
			}
		}
	}
}
