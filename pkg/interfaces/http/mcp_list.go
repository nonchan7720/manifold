package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
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
}

func NewMCPHandler(
	cfg config.Servers, catalog ToolCataloger, authzCfg config.AuthzConfig,
) *MCPHandler {
	servers := make([]*config.Server, 0, len(cfg))
	for _, srv := range cfg {
		servers = append(servers, srv)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return &MCPHandler{servers: servers, catalog: catalog, authzCfg: authzCfg}
}

// isToolCatalogAdmin reports whether r's identity headers place it in one of
// h.authzCfg.AdminGroups. Fails closed: a missing/unresolvable identity or
// an empty AdminGroups both deny.
func (h *MCPHandler) isToolCatalogAdmin(r *http.Request) bool {
	if len(h.authzCfg.AdminGroups) == 0 {
		return false
	}
	principal, err := authz.PrincipalFromHeader(r.Header, h.authzCfg.Headers)
	if err != nil {
		return false
	}
	for _, g := range principal.Groups {
		if slices.Contains(h.authzCfg.AdminGroups, g) {
			return true
		}
	}
	return false
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
	if includeTools && h.authzCfg.Enabled && !h.isToolCatalogAdmin(r) {
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
