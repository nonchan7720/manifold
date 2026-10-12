package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

type ToolFunc func(ctx context.Context, input map[string]any) (body []byte, contentType string, _ error)

type Tool struct {
	tool    mcp.Tool
	handler ToolFunc
	// method/path hold the source operation for Definitions(), not mcp.Tool itself.
	method string
	path   string
	// summary/description hold the source operation's OpenAPI summary and
	// description verbatim (each empty when the spec omits it), for the
	// /mcp/list?tools=true catalog. mcp.Tool.Description is the derived
	// description exposed to MCP clients, which may fall back to one of these
	// or to "METHOD /path".
	summary     string
	description string
	// binaryFields は 2xx の JSON レスポンス内の format: binary フィールドの位置。
	// メディアストレージ有効時に URL へ置き換えるために使う（spec から導出するので
	// generated catalog 経由でも同じ値になる）。
	binaryFields []oastomcptool.BinaryField
}

// ToolInfo is the catalog entry of a registered tool for /mcp/list?tools=true.
// For an OpenAPI tool, Summary and Description are the source operation's
// summary and description verbatim (empty when the spec omits them). For a
// tool from another kind of backend, Description is the tool's description
// and Summary is empty.
type ToolInfo struct {
	Name        string
	Summary     string
	Description string
}

type MCPToolRegistry struct {
	mu       sync.RWMutex
	tools    map[string]Tool
	specHash string
	// openAPI is the parsed OpenAPI 3.x document the tools were built from,
	// kept as the base for breaking-change detection on spec refresh. nil
	// for Swagger 2.x.
	openAPI *openapi3.T
	// baseURL is the API base URL the tools send their requests to: the
	// configured mcpServers.<name>.baseURL, or the one derived from the spec
	// when that is unset (see BaseURL).
	baseURL string
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
// generated from (method is upper-cased), for later readback via Definitions(),
// and sets the tool annotations (read-only / write / destructive) derived
// from the HTTP method.
func WithRegisterToolOperation(method, path string) RegisterToolOptions {
	return func(tool *Tool) {
		tool.method = strings.ToUpper(method)
		tool.path = path
		applyMethodAnnotations(&tool.tool, tool.method)
	}
}

// applyMethodAnnotations sets the MCP tool annotations from the HTTP method:
// safe methods (GET/HEAD/OPTIONS/TRACE) are read-only, DELETE is destructive,
// and every other method (POST/PUT/PATCH, ...) is a non-destructive write.
func applyMethodAnnotations(t *mcp.Tool, method string) {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		applyReadOnly(t)
	case http.MethodDelete:
		applyDestructive(t)
	default:
		applyWrite(t)
	}
}

// Annotation kinds accepted by x-mcp.annotations.kind.
const (
	annotationKindReadOnly    = "readOnly"
	annotationKindWrite       = "write"
	annotationKindDestructive = "destructive"
)

// WithRegisterToolAnnotationsExtension applies the operation's
// x-mcp.annotations extension on top of the method-derived annotations, so
// it must come after WithRegisterToolOperation. Use
// ParseAnnotationsExtension to validate the extension first.
func WithRegisterToolAnnotationsExtension(ext AnnotationsExtension) RegisterToolOptions {
	return func(tool *Tool) {
		ext.apply(&tool.tool)
	}
}

// AnnotationsExtension is the parsed x-mcp.annotations of an operation:
//
//	x-mcp:
//	  annotations:
//	    kind: readOnly | write | destructive # replaces the method-derived preset
//	    readOnlyHint: true                   # individual hints override the preset
//	    destructiveHint: false
//	    idempotentHint: true
//	    openWorldHint: false
//	    title: Find pet
type AnnotationsExtension struct {
	kind            string
	readOnlyHint    *bool
	destructiveHint *bool
	idempotentHint  *bool
	openWorldHint   *bool
	title           *string
}

// ParseAnnotationsExtension reads x-mcp.annotations from an operation's
// extensions. It returns the zero value (which changes nothing) when the
// extension is absent, and an error when it is malformed (unknown kind or
// key, or a value of the wrong type).
func ParseAnnotationsExtension(extensions map[string]any) (AnnotationsExtension, error) {
	var ext AnnotationsExtension
	xmcp, ok := extensions["x-mcp"]
	if !ok || xmcp == nil {
		return ext, nil
	}
	xm, ok := xmcp.(map[string]any)
	if !ok {
		return ext, fmt.Errorf("x-mcp must be an object, got %T", xmcp)
	}
	raw, ok := xm["annotations"]
	if !ok || raw == nil {
		return ext, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return ext, fmt.Errorf("x-mcp.annotations must be an object, got %T", raw)
	}

	for key, v := range m {
		switch key {
		case "kind":
			kind, ok := v.(string)
			if !ok {
				return ext, fmt.Errorf("x-mcp.annotations.kind must be a string, got %T", v)
			}
			switch kind {
			case annotationKindReadOnly, annotationKindWrite, annotationKindDestructive:
				ext.kind = kind
			default:
				return ext, fmt.Errorf(
					"x-mcp.annotations.kind: unknown kind %q (want %s, %s or %s)",
					kind, annotationKindReadOnly, annotationKindWrite, annotationKindDestructive,
				)
			}
		case "readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint":
			b, ok := v.(bool)
			if !ok {
				return ext, fmt.Errorf("x-mcp.annotations.%s must be a boolean, got %T", key, v)
			}
			switch key {
			case "readOnlyHint":
				ext.readOnlyHint = &b
			case "destructiveHint":
				ext.destructiveHint = &b
			case "idempotentHint":
				ext.idempotentHint = &b
			case "openWorldHint":
				ext.openWorldHint = &b
			}
		case "title":
			title, ok := v.(string)
			if !ok {
				return ext, fmt.Errorf("x-mcp.annotations.title must be a string, got %T", v)
			}
			ext.title = &title
		default:
			return ext, fmt.Errorf("x-mcp.annotations: unknown key %q", key)
		}
	}
	return ext, nil
}

func (e AnnotationsExtension) isZero() bool {
	return e == AnnotationsExtension{}
}

func (e AnnotationsExtension) apply(t *mcp.Tool) {
	if e.isZero() {
		return
	}
	switch e.kind {
	case annotationKindReadOnly:
		resetAnnotations(t)
		applyReadOnly(t)
	case annotationKindWrite:
		resetAnnotations(t)
		applyWrite(t)
	case annotationKindDestructive:
		resetAnnotations(t)
		applyDestructive(t)
	}
	a := toolAnnotations(t)
	if e.readOnlyHint != nil {
		a.ReadOnlyHint = *e.readOnlyHint
	}
	if e.destructiveHint != nil {
		a.DestructiveHint = new(*e.destructiveHint)
	}
	if e.idempotentHint != nil {
		a.IdempotentHint = *e.idempotentHint
	}
	if e.openWorldHint != nil {
		a.OpenWorldHint = new(*e.openWorldHint)
	}
	if e.title != nil {
		a.Title = *e.title
	}
}

// resetAnnotations clears the hints a kind preset sets, so switching e.g. a
// GET from readOnly to write does not keep its IdempotentHint.
func resetAnnotations(t *mcp.Tool) {
	a := toolAnnotations(t)
	a.ReadOnlyHint = false
	a.DestructiveHint = nil
	a.IdempotentHint = false
}

func toolAnnotations(t *mcp.Tool) *mcp.ToolAnnotations {
	if t.Annotations == nil {
		t.Annotations = &mcp.ToolAnnotations{}
	}
	return t.Annotations
}

func applyReadOnly(t *mcp.Tool) {
	a := toolAnnotations(t)
	a.ReadOnlyHint = true
	a.DestructiveHint = new(false)
	a.IdempotentHint = true
}

func applyWrite(t *mcp.Tool) {
	a := toolAnnotations(t)
	a.ReadOnlyHint = false
	a.DestructiveHint = new(false)
}

func applyDestructive(t *mcp.Tool) {
	a := toolAnnotations(t)
	a.ReadOnlyHint = false
	a.DestructiveHint = new(true)
}

// WithRegisterToolDocs records the source operation's OpenAPI summary and
// description verbatim, for readback via ToolInfo (the /mcp/list catalog).
func WithRegisterToolDocs(summary, description string) RegisterToolOptions {
	return func(tool *Tool) {
		tool.summary = summary
		tool.description = description
	}
}

// WithRegisterToolBinaryFields は JSON レスポンス内のバイナリフィールドの位置を記録する。
func WithRegisterToolBinaryFields(fields []oastomcptool.BinaryField) RegisterToolOptions {
	return func(tool *Tool) {
		tool.binaryFields = fields
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

// SpecHash returns the hash of the spec these tools were built from.
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

// OpenAPISpec returns the OpenAPI 3.x document these tools were built from,
// or nil for a Swagger 2.x spec.
func (r *MCPToolRegistry) OpenAPISpec() *openapi3.T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.openAPI
}

func (r *MCPToolRegistry) setOpenAPISpec(spec *openapi3.T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.openAPI = spec
}

// BaseURL returns the API base URL the registered tools call. It is empty,
// or relative, when baseURL was not configured and the spec gave nothing to
// derive one from (no servers / host entry, and a spec path that is not an
// http(s) URL) — every tools/call would then fail, which is why the gateway
// refuses to serve such a catalog (see errBaseURLUnresolved).
func (r *MCPToolRegistry) BaseURL() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.baseURL
}

func (r *MCPToolRegistry) setBaseURL(baseURL string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.baseURL = baseURL
}

// ListTools returns all registered tools sorted by name.
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

// ToolDefinition is a read-only, display-friendly view of a registered tool.
type ToolDefinition struct {
	Name           string
	Method         string // upper-case, e.g. "GET"
	Path           string // e.g. "/pet/{petId}"
	Description    string
	InputSchema    map[string]any
	BinaryResponse bool
}

// Definitions returns the ToolDefinition for every registered tool, sorted by name.
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

// toolBinaryResponse reports whether tool carries the
// _meta.manifold.binaryResponse marker.
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

// objectBody returns b as a result's structuredContent when it is a JSON
// object, which is all MCP 2025-06-18 allows there (the TypeScript SDK
// rejects an array or scalar). Arrays are wrapped as {"items": [...]} by
// wrapIfArray before they get here; whatever is left is not structured.
// A response declared as something other than JSON (text/plain, ...) is
// never structured, even when its body parses as an object; one without a
// Content-Type is judged by its body alone.
func objectBody(contentType string, b []byte) (json.RawMessage, bool) {
	if contentType != "" && !isJSONContentType(contentType) {
		return nil, false
	}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, false
	}
	return json.RawMessage(trimmed), true
}
