package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
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
	mu       sync.RWMutex
	tools    map[string]Tool
	specHash string
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

func (r *MCPToolRegistry) RegisterTool(
	name, description string,
	inputSchema map[string]any,
	handler ToolFunc,
	opts ...RegisterToolOptions,
) {
	tool := Tool{
		tool: mcp.Tool{
			Name:        name,
			Description: description,
			InputSchema: inputSchema,
		},
		handler: wrapToolFunc(handler),
	}
	for _, fn := range opts {
		fn(&tool)
	}
	r.tools[name] = tool
}

// SpecHash returns the hash of the spec these tools were built from. It is
// empty for registries that were not built from a spec.
func (r *MCPToolRegistry) SpecHash() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.specHash
}

func (r *MCPToolRegistry) setSpecHash(hash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specHash = hash
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

func wrapToolFunc(tool ToolFunc) ToolFunc {
	return func(ctx context.Context, input map[string]any) ([]byte, string, error) {
		resp, contentType, err := tool(ctx, input)
		if err != nil {
			return nil, "", err
		}
		mediaType, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			// content type のパースに失敗した場合はそのまま返す
			return resp, contentType, nil //nolint: nilerr
		}
		profileValue, isProfile := params["profile"]
		if mediaType == "application/json" || (isProfile && profileValue == "application/json") {
			if v, err := wrapIfArray(resp); err != nil {
				return nil, "", err
			} else {
				// profile があれば profile 側を使用する
				if isProfile {
					contentType = profileValue
				}
				return v, contentType, nil
			}
		}
		return resp, contentType, nil
	}
}

func wrapIfArray(b []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return b, nil
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("invalid json")
	}
	wrapped := map[string]json.RawMessage{
		"items": json.RawMessage(b),
	}
	return json.Marshal(wrapped)
}
