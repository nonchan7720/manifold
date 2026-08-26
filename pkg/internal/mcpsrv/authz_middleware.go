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
)

// errToolNotAllowedByPolicy is the only detail returned to clients for any
// authz denial (missing identity, policy deny, or a Decider failure) — the
// reason is logged server-side, never echoed back.
var errToolNotAllowedByPolicy = &jsonrpc.Error{
	Code:    jsonrpc.CodeInternalError,
	Message: "tool not allowed by policy",
}

// authzPrincipal resolves the calling Principal from req's HTTP headers.
// A nil Extra (e.g. non-HTTP transports) is treated the same as a missing
// identity header.
func authzPrincipal(
	req mcp.Request, headers config.AuthzHeaders,
) (authz.Principal, error) {
	extra := req.GetExtra()
	if extra == nil {
		return authz.Principal{}, authz.ErrMissingIdentity
	}
	return authz.PrincipalFromHeader(extra.Header, headers)
}

// authzHandleToolCall enforces d.Allow for a tools/call request, forwarding
// to next only when allowed. Any non-allow outcome (deny, Decider error, or
// an unexpected params type) maps to the same fixed JSON-RPC error.
func authzHandleToolCall(
	ctx context.Context,
	serverName string,
	d authz.Decider,
	p authz.Principal,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
) (mcp.Result, error) {
	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok {
		slog.ErrorContext(ctx, "authz: unexpected params type on tools/call",
			slog.String("server", serverName), slog.String("reason", "unexpected_params"))
		return nil, errToolNotAllowedByPolicy
	}
	tool := authz.ToolRef{Server: serverName, Name: params.Name}

	allowed, err := d.Allow(ctx, p, tool)
	if err != nil {
		slog.ErrorContext(ctx, "authz: decider error on tools/call",
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
		slog.String("server", serverName), slog.String("tool", tool.Name),
		slog.String("decision", decision))

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
	serverName string,
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
		refs[i] = authz.ToolRef{Server: serverName, Name: tool.Name}
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
		slog.String("server", serverName), slog.String("method", authzMethodToolsList),
		slog.Int("allowed", len(result.Tools)), slog.Int("total", len(refs)))
	return result, nil
}

// NewAuthzMiddleware builds a mcp.Middleware enforcing d as the PEP for
// server serverName. It fails closed: a missing/unresolvable identity denies
// without querying d, and any tools/call or tools/list Decider error also
// denies rather than falling through to next.
func NewAuthzMiddleware(
	serverName string, d authz.Decider, headers config.AuthzHeaders,
) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != authzMethodToolsCall && method != authzMethodToolsList {
				return next(ctx, method, req)
			}

			p, err := authzPrincipal(req, headers)
			if err != nil {
				slog.InfoContext(ctx, "authz decision",
					slog.String("server", serverName), slog.String("method", method),
					slog.String("decision", "deny"), slog.String("reason", "missing_identity"))
				return nil, errToolNotAllowedByPolicy
			}

			if method == authzMethodToolsCall {
				return authzHandleToolCall(ctx, serverName, d, p, next, method, req)
			}
			return authzHandleToolsList(ctx, serverName, d, p, next, method, req)
		}
	}
}
