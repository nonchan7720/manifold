package mcpsrv

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
)

// ServerToolMiddlewares returns the tool middlewares every server gets, in
// the order AddReceivingMiddleware expects within one call (outermost first):
//
//	audit → tool search → authz (from authzMiddlewares) → cache → tool filter
//
// The tool filter sits right outside the backend so every outer layer sees
// the exposed names; the cache sits inside authz so a cached result is only
// returned to a caller allowed to call the tool; tool search sits outside
// authz so it only ever searches (and counts against its threshold) the tools
// authz lets the caller see; audit sits outside everything so denied calls
// and tool_search calls are recorded too.
func ServerToolMiddlewares(
	name string,
	server *config.Server,
	authzMiddlewares []mcp.Middleware,
	cache *ToolCache,
	audit *AuditLogger,
	search config.ToolSearchConfig,
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
	out = append(out, newToolSearchMiddleware(name, search))
	out = append(out, authzMiddlewares...)
	if m := newToolCacheMiddleware(name, cacheCf, cache); m != nil {
		out = append(out, m)
	}
	if m := newToolFilterMiddleware(tools); m != nil {
		out = append(out, m)
	}
	return out
}
