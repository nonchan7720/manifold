package httphandler

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/nonchan7720/manifold/pkg/config"
)

type MCPHandler struct {
	servers []*config.Server
}

func NewMCPHandler(cfg config.Servers) *MCPHandler {
	servers := make([]*config.Server, 0, len(cfg))
	for _, srv := range cfg {
		servers = append(servers, srv)
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
	return &MCPHandler{servers: servers}
}

func (h *MCPHandler) MCPList(w http.ResponseWriter, r *http.Request) {
	type mcpServer struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	type response struct {
		MCP []mcpServer `json:"mcp"`
	}
	resp := response{
		MCP: make([]mcpServer, len(h.servers)),
	}
	for idx, srv := range h.servers {
		resp.MCP[idx] = mcpServer{
			Name:        srv.Name,
			Description: srv.Description,
		}
	}
	w.Header().Add("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&resp)
}
