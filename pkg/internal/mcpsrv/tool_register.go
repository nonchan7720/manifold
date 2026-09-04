package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"slices"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ToolFunc func(ctx context.Context, input map[string]any) (body []byte, contentType string, _ error)

type Tool struct {
	tool    mcp.Tool
	handler ToolFunc
	// method/path は WithRegisterToolOperation で設定される、生成元の
	// operation（例: "GET", "/pet/{petId}"）。CLI からツール定義を読み出す
	// Definitions() のためだけに保持し、mcp.Tool 自体には含めない。
	method string
	path   string
}

// ToolInfo is the (name, description) pair of a registered tool, independent
// of the input schema and handler — used to build the admin tool catalog
// (MCPServer.ToolCatalog) without exposing mcp.Tool internals.
type ToolInfo struct {
	Name        string
	Description string
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

// WithRegisterToolOperation records the HTTP method and path the tool was
// generated from (method is upper-cased), for later readback via Definitions().
func WithRegisterToolOperation(method, path string) RegisterToolOptions {
	return func(tool *Tool) {
		tool.method = strings.ToUpper(method)
		tool.path = path
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

// ListTools returns all registered tools sorted by name. The sort makes tool
// order deterministic for callers that display or diff it (e.g. the
// `manifold openapi tools` CLI).
func (r *MCPToolRegistry) ListTools() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	listTools := make([]Tool, len(r.tools))
	toolIdx := 0
	for _, tool := range r.tools {
		listTools[toolIdx] = tool
		toolIdx++
	}
	slices.SortFunc(listTools, func(a, b Tool) int {
		return strings.Compare(a.tool.Name, b.tool.Name)
	})
	return listTools
}

// ToolDefinition is a read-only, display-friendly view of a registered tool:
// name, the operation it was generated from, description, inputSchema, and
// whether it is treated as a binary response. It exists so a caller outside
// this package (e.g. a future `manifold openapi tools` CLI) can read back
// exactly what RegisterOpenAPI/BuildCatalog built, without depending on
// mcp.Tool internals.
type ToolDefinition struct {
	Name           string
	Method         string // upper-case, e.g. "GET"
	Path           string // e.g. "/pet/{petId}"
	Description    string
	InputSchema    map[string]any
	BinaryResponse bool
}

// Definitions returns the ToolDefinition for every registered tool, sorted
// by name (see ListTools).
func (r *MCPToolRegistry) Definitions() []ToolDefinition {
	tools := r.ListTools()
	defs := make([]ToolDefinition, len(tools))
	for i, t := range tools {
		schema, _ := t.tool.InputSchema.(map[string]any)
		defs[i] = ToolDefinition{
			Name:           t.tool.Name,
			Method:         t.method,
			Path:           t.path,
			Description:    t.tool.Description,
			InputSchema:    schema,
			BinaryResponse: toolBinaryResponse(t.tool),
		}
	}
	return defs
}

// toolBinaryResponse reports whether tool carries the same
// _meta.manifold.binaryResponse marker WithRegisterToolMeta sets in the
// openapi3 loop's binary-response case.
func toolBinaryResponse(tool mcp.Tool) bool {
	manifoldMeta, ok := tool.Meta["manifold"].(map[string]any)
	if !ok {
		return false
	}
	binary, _ := manifoldMeta["binaryResponse"].(bool)
	return binary
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
