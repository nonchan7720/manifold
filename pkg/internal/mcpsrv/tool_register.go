package mcpsrv

import (
	"context"
	"maps"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolFunc func(ctx context.Context, input map[string]any) (body []byte, contentType string, _ error)

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

type RegisterToolOptions func(tool *Tool)

func WithRegisterToolMeta(meta map[string]any) RegisterToolOptions {
	return func(tool *Tool) {
		if tool.tool.Meta == nil {
			tool.tool.Meta = make(mcp.Meta)
		}
		maps.Copy(tool.tool.Meta, meta)
	}
}

func (r *MCPToolRegistry) RegisterTool(name, description string, inputSchema map[string]any, handler ToolFunc, opts ...RegisterToolOptions) {
	tool := Tool{
		tool: mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: inputSchema,
		},
		handler: handler,
	}
	for _, fn := range opts {
		fn(&tool)
	}
	r.tools[name] = tool
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
