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
// Each of audit, tool search, cache and tool filter is left out when its
// configuration doesn't turn it on (no audit logger, no
// gateway.toolSearch.enabled, no cache settings, no include / exclude /
// overrides).
// The tool filter sits right outside the backend so every outer layer sees
// the exposed names (it leaves the gateway's own create_pairing_code and
// the <agent>__<skill> tools of agents alone); the cache sits inside authz so a cached result is only
// returned to a caller allowed to call the tool; tool search sits outside
// authz so it only ever searches (and counts against its threshold) the tools
// authz lets the caller see; audit sits outside everything so denied calls
// and tool_search calls are recorded too. authzKey (see NewAuthzCacheKeyer)
// lets tool search key its per-caller index cache by what authz decides on;
// with authz middlewares but no authzKey, tool search doesn't cache at all.
func ServerToolMiddlewares(
	name string,
	server *config.Server,
	authzMiddlewares []mcp.Middleware,
	authzKey AuthzCacheKeyer,
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
	if len(authzMiddlewares) > 0 && authzKey == nil {
		// authz decides per principal but we can't tell principals apart:
		// tool_search must not reuse an index across requests.
		authzKey = func(mcp.Request) (string, bool) { return "", false }
	}
	var out []mcp.Middleware
	if m := newAuditMiddleware(name, service, audit); m != nil {
		out = append(out, m)
	}
	if m := newToolSearchMiddleware(name, search, cache, authzKey); m != nil {
		out = append(out, m)
	}
	out = append(out, authzMiddlewares...)
	if m := newToolCacheMiddleware(name, cacheCf, cache); m != nil {
		out = append(out, m)
	}
	var builtin []string
	if server != nil && server.IsReverseBackend() {
		// create_pairing_code is registered by the gateway itself
		// (registerPairingTool), not by the tab: it has to stay listed and
		// callable whatever tools.include / exclude say, or nobody can pair.
		builtin = append(builtin, createPairingCodeToolName)
	}
	if server != nil {
		// <agent>__<skill> tools come from mcpServers.<name>.agents, not the
		// backend: matched by their <agent>__ prefix (see toolFilter.builtinPrefixes).
		for agent := range server.Agents {
			builtin = append(builtin, config.AgentToolName(agent, ""))
		}
	}
	if m := newToolFilterMiddleware(tools, builtin...); m != nil {
		out = append(out, m)
	}
	return out
}
