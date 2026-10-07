package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// ServerToolMiddlewares returns the tool middlewares every server gets, in
// the order AddReceivingMiddleware expects within one call (outermost first):
//
//	audit → authz (from authzMiddlewares) → cache → inner → tool filter
//
// Each of audit, cache and tool filter is left out when its configuration
// doesn't turn it on (no audit logger, no cache settings, no include /
// exclude / overrides).
// The tool filter sits right outside the backend so every outer layer sees
// the exposed names (on a reverse server it leaves the gateway's own
// create_pairing_code alone); inner (the service agents middleware) sits
// outside the filter, so the <agent>__<skill> tools it adds are never
// filtered or renamed while a backend tool that merely shares their prefix
// still is; the cache sits inside authz so a cached result is only
// returned to a caller allowed to call the tool; audit sits outside authz so
// denied calls are recorded too.
func ServerToolMiddlewares(
	name string,
	server *config.Server,
	authzMiddlewares []mcp.Middleware,
	cache *ToolCache,
	audit *AuditLogger,
	inner ...mcp.Middleware,
) []mcp.Middleware {
	var (
		tools   *config.ToolsConfig
		cacheCf *config.CacheConfig
		service = name
	)
	if server != nil {
		tools, cacheCf, service = server.Tools, server.Cache, server.ServiceCode()
	}
	var out []mcp.Middleware
	if m := newAuditMiddleware(name, service, audit); m != nil {
		out = append(out, m)
	}
	out = append(out, authzMiddlewares...)
	if m := newToolCacheMiddleware(name, cacheCf, cache); m != nil {
		out = append(out, m)
	}
	out = append(out, inner...)
	var builtin []string
	if server != nil && server.IsReverseBackend() {
		// create_pairing_code is registered by the gateway itself
		// (registerPairingTool), not by the tab: it has to stay listed and
		// callable whatever tools.include / exclude say, or nobody can pair.
		builtin = append(builtin, createPairingCodeToolName)
	}
	if m := newToolFilterMiddleware(tools, builtin...); m != nil {
		out = append(out, m)
	}
	return out
}
