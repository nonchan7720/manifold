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

// authzListSpec は authzHandleList が一覧系メソッドの結果を絞り込むための、
// 結果の型 R と要素の型 T ごとの差分をまとめたもの。
type authzListSpec[R mcp.Result, T any] struct {
	// method はログに出すメソッド名。
	method string
	// items は結果から絞り込み対象の要素を取り出す。
	items func(R) []T
	// refName は要素を authz の判定に渡すときの名前を返す。
	refName func(T) string
	// withItems は結果の要素を filtered に置き換えた結果を返す。
	withItems func(result R, filtered []T) R
}

// authzToolsListSpec は tools/list の結果をツール名で判定する。結果はその場で
// 書き換える。
var authzToolsListSpec = authzListSpec[*mcp.ListToolsResult, *mcp.Tool]{
	method:  authzMethodToolsList,
	items:   func(r *mcp.ListToolsResult) []*mcp.Tool { return r.Tools },
	refName: func(t *mcp.Tool) string { return t.Name },
	withItems: func(r *mcp.ListToolsResult, filtered []*mcp.Tool) *mcp.ListToolsResult {
		r.Tools = filtered
		return r
	},
}

// authzPromptsListSpec は prompts/list の結果を、名前に authzPromptPrefix を
// 付けてツールと同じ形で判定する。
var authzPromptsListSpec = authzListSpec[*mcp.ListPromptsResult, *mcp.Prompt]{
	method:  authzMethodPromptsList,
	items:   func(r *mcp.ListPromptsResult) []*mcp.Prompt { return r.Prompts },
	refName: func(prompt *mcp.Prompt) string { return authzPromptPrefix + prompt.Name },
	withItems: func(r *mcp.ListPromptsResult, filtered []*mcp.Prompt) *mcp.ListPromptsResult {
		// 結果はパススルーの取得元やバックエンドと共有している可能性があるため複製する。
		copied := *r
		copied.Prompts = filtered
		return &copied
	},
}

// authzHandleList は next で一覧を取得し、spec が取り出す要素を d.AllowedTools が
// 許可したものに順序を保って絞る。結果が R でない場合や空の一覧では Decider を
// 呼ばずにそのまま返す。Decider のエラーは固定の JSON-RPC エラーにする。
func authzHandleList[R mcp.Result, T any](
	ctx context.Context,
	serverName, serviceCode string,
	d authz.Decider,
	p authz.Principal,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
	spec authzListSpec[R, T],
) (mcp.Result, error) {
	res, err := next(ctx, method, req)
	if err != nil {
		return nil, err
	}
	result, ok := res.(R)
	if !ok {
		return res, nil
	}
	items := spec.items(result)
	if len(items) == 0 {
		return res, nil
	}

	refs := make([]authz.ToolRef, len(items))
	for i, item := range items {
		refs[i] = authz.ToolRef{Server: serverName, Service: serviceCode, Name: spec.refName(item)}
	}
	allowed, err := d.AllowedTools(ctx, p, refs)
	if err != nil {
		slog.ErrorContext(ctx, "authz: decider error on "+spec.method,
			slog.String("server", serverName), slog.Any("error", err))
		return nil, errToolNotAllowedByPolicy
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, t := range allowed {
		allowedSet[t.Name] = struct{}{}
	}
	filtered := make([]T, 0, len(allowed))
	for _, item := range items {
		if _, ok := allowedSet[spec.refName(item)]; ok {
			filtered = append(filtered, item)
		}
	}

	slog.InfoContext(ctx, "authz decision",
		slog.String("user", p.UserID), slog.Any("groups", p.Groups),
		slog.String("server", serverName), slog.String("service", serviceCode),
		slog.String("method", spec.method),
		slog.Int("allowed", len(filtered)), slog.Int("total", len(refs)))
	return spec.withItems(result, filtered), nil
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
	return authzHandleList(
		ctx,
		serverName,
		serviceCode,
		d,
		p,
		next,
		method,
		req,
		authzToolsListSpec,
	)
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
	return authzHandleList(
		ctx, serverName, serviceCode, d, p, next, method, req, authzPromptsListSpec,
	)
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
