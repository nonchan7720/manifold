package httphandler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/services/authz"
)

// ToolCataloger resolves a server's full tool catalog for the admin listing
// (?tools=true), independent of the per-caller tools/list authz filtering
// NewAuthzMiddleware applies on the mcp.Server path.
type ToolCataloger interface {
	ToolCatalog(ctx context.Context, name string) ([]mcpsrv.ToolInfo, error)
}

type mcpToolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type mcpServerEntry struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Tools       *[]mcpToolEntry `json:"tools,omitempty"`
	Dynamic     bool            `json:"dynamic,omitempty"`
	Error       string          `json:"error,omitempty"`
}

type MCPHandler struct {
	servers  []*config.Server
	catalog  ToolCataloger
	authzCfg config.AuthzConfig
	decider  authz.Decider
}

// NewMCPHandler builds an MCPHandler. decider is nil when authzCfg.Enabled
// is false; it must be non-nil whenever authzCfg.Enabled is true, since
// allowToolCatalog denies (fails closed) rather than dereferencing a nil
// Decider.
func NewMCPHandler(
	cfg config.Servers, catalog ToolCataloger, authzCfg config.AuthzConfig, decider authz.Decider,
) *MCPHandler {
	servers := make([]*config.Server, 0, len(cfg))
	for _, srv := range cfg {
		servers = append(servers, srv)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return &MCPHandler{servers: servers, catalog: catalog, authzCfg: authzCfg, decider: decider}
}

// allowToolCatalog reports whether r may read the unfiltered tool catalog,
// delegating the decision to h.decider. Fails closed: a missing/unresolvable
// identity denies without querying the Decider, and a nil Decider or a
// Decider error both deny. The 403 body stays generic, so the reason (which
// names the header at fault) is only ever logged.
func (h *MCPHandler) allowToolCatalog(ctx context.Context, r *http.Request) bool {
	principal, err := authz.PrincipalFromHeader(
		r.Header, h.authzCfg.Headers, h.authzCfg.Input.FromHeaders,
	)
	if err != nil {
		slog.InfoContext(ctx, "authz decision",
			slog.String("method", "GET /mcp/list?tools=true"),
			slog.String("decision", "deny"),
			slog.String("reason", authz.DenyReason(err)), slog.Any("error", err))
		return false
	}
	if h.decider == nil {
		return false
	}
	allowed, err := h.decider.AllowCatalog(ctx, principal)
	if err != nil {
		return false
	}
	return allowed
}

// toolCatalogFields resolves the tools/dynamic/error portion of srv's
// listing entry. Reverse-transport servers have no catalog until a browser
// connects, so they're reported as dynamic instead of queried.
func (h *MCPHandler) toolCatalogFields(
	ctx context.Context, srv *config.Server,
) (tools *[]mcpToolEntry, dynamic bool, errMsg string) {
	if srv.IsReverseBackend() {
		return nil, true, ""
	}
	infos, err := h.catalog.ToolCatalog(ctx, srv.Name)
	if err != nil {
		return nil, false, err.Error()
	}
	entries := make([]mcpToolEntry, len(infos))
	for i, info := range infos {
		entries[i] = mcpToolEntry{Name: info.Name, Description: info.Description}
	}
	return &entries, false, ""
}

func (h *MCPHandler) MCPList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx = trace.StartSpan(ctx, "MCPHandler/MCPList")
	var err error
	defer func() { trace.EndSpan(ctx, err) }()

	includeTools := r.URL.Query().Get("tools") == "true"
	if includeTools && h.authzCfg.Enabled &&
		!authz.BypassRequested(r.Header, h.authzCfg.Headers) && !h.allowToolCatalog(ctx, r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		return
	}

	type response struct {
		MCP []mcpServerEntry `json:"mcp"`
	}
	resp := response{MCP: make([]mcpServerEntry, len(h.servers))}
	for idx, srv := range h.servers {
		entry := mcpServerEntry{Name: srv.Name, Description: srv.Description}
		if includeTools {
			entry.Tools, entry.Dynamic, entry.Error = h.toolCatalogFields(ctx, srv)
		}
		resp.MCP[idx] = entry
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&resp)
}
