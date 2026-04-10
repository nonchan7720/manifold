package mcpsrv

import (
	"context"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolFunc func(ctx context.Context, input map[string]any) (string, error)

type Tool struct {
	tool    mcp.Tool
	handler ToolFunc
}

type MCPToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

func NewMCPToolRegistry() *MCPToolRegistry {
	return &MCPToolRegistry{
		tools: map[string]Tool{},
	}
}

func (r *MCPToolRegistry) RegisterTool(name, description string, inputSchema map[string]any, handler ToolFunc) {
	r.tools[name] = Tool{
		tool: mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: inputSchema,
		},
		handler: handler,
	}
}

func (r *MCPToolRegistry) GetTool(name string) *Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if v, ok := r.tools[name]; ok {
		return &v
	}
	return nil
}

func (r *MCPToolRegistry) ListTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	listTools := make([]Tool, len(r.tools))
	toolIdx := 0
	for _, tool := range r.tools {
		listTools[toolIdx] = tool
		toolIdx++
	}
	sort.SliceIsSorted(listTools, func(i, j int) bool {
		return listTools[i].tool.Name < listTools[j].tool.Name
	})
	return listTools
}
