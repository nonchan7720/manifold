package mcpsrv

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addToolSafely registers t on srv like (*mcp.Server).AddTool, but returns an
// error instead of panicking when the definition is one AddTool rejects.
//
// Tool definitions reach AddTool from places the gateway does not fully
// control (a tab's tools/list, a fetched OpenAPI spec), and AddTool often runs
// outside any HTTP handler (an edge WebSocket goroutine, the SDK's read
// goroutine, a spec refetch), where middleware.Recover never sees a panic.
// So the definition is checked up front with validateToolDefinition, and the
// call itself is guarded by recover as a last line of defence against checks
// a future SDK version adds.
func addToolSafely(srv *mcp.Server, t *mcp.Tool, h mcp.ToolHandler) (rErr error) {
	if srv == nil {
		return errors.New("server is nil")
	}
	if h == nil {
		return errors.New("tool handler is nil")
	}
	if err := validateToolDefinition(t); err != nil {
		return err
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("AddTool panicked; tool skipped",
				slog.String("tool", t.Name), slog.Any("panic", r),
				slog.String("stack", string(debug.Stack())))
			rErr = fmt.Errorf("add tool %q: %v", t.Name, r)
		}
	}()
	srv.AddTool(t, h)
	return nil
}

// validateToolDefinition mirrors the checks (*mcp.Server).AddTool panics on
// (go-sdk v1.8.0), so a bad definition can be rejected before it reaches
// AddTool. Both schema fields are compared after a JSON round trip, which
// covers every representation the SDK accepts (map[string]any,
// json.RawMessage, *jsonschema.Schema, ...).
func validateToolDefinition(t *mcp.Tool) error {
	if t == nil {
		return errors.New("tool is nil")
	}
	if t.InputSchema == nil {
		return errors.New("missing input schema")
	}
	var input map[string]any
	if err := remarshalJSON(t.InputSchema, &input); err != nil {
		return fmt.Errorf("input schema is not a JSON object: %w", err)
	}
	if typ, _ := input["type"].(string); typ != "object" {
		return fmt.Errorf(`input schema must have type "object" (got %v)`, input["type"])
	}
	if t.OutputSchema != nil {
		var output any
		if err := remarshalJSON(t.OutputSchema, &output); err != nil {
			return fmt.Errorf("output schema is not valid JSON: %w", err)
		}
	}
	if err := validateParamHeaderAnnotations(t.InputSchema); err != nil {
		return fmt.Errorf("invalid parameter header annotations: %w", err)
	}
	return nil
}

// remarshalJSON converts from through JSON into to.
func remarshalJSON(from, to any) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}

// headerSchemaProperty captures the fields the SDK's x-mcp-header check reads
// from an input schema property.
type headerSchemaProperty struct {
	Type       string                          `json:"type"`
	XMCPHeader json.RawMessage                 `json:"x-mcp-header,omitempty"`
	Properties map[string]headerSchemaProperty `json:"properties,omitempty"`
}

// validateParamHeaderAnnotations mirrors the SDK's check of x-mcp-header
// annotations (AddTool panics when it fails): an annotation may appear on a
// property at any depth, must sit on a primitive (string / integer /
// boolean) property, must be a non-empty HTTP token, and must be unique
// case-insensitively across the whole schema. A schema the SDK cannot decode
// into this shape is skipped, as the SDK does.
func validateParamHeaderAnnotations(schema any) error {
	var root headerSchemaProperty
	if err := remarshalJSON(schema, &root); err != nil {
		return nil //nolint:nilerr // the SDK skips the check for such schemas too
	}
	if len(root.Properties) == 0 {
		return nil
	}
	return validateParamHeadersIn(root.Properties, "", map[string]bool{})
}

func validateParamHeadersIn(
	props map[string]headerSchemaProperty,
	prefix string,
	seen map[string]bool,
) error {
	for propName, prop := range props {
		path := propName
		if prefix != "" {
			path = prefix + "." + propName
		}
		if prop.XMCPHeader != nil {
			if prop.Type != "string" && prop.Type != "integer" && prop.Type != "boolean" {
				return fmt.Errorf(
					"property %q: x-mcp-header can only be applied to primitive types "+
						"(integer, string, boolean), got %q", path, prop.Type,
				)
			}
			var headerName string
			if err := json.Unmarshal(prop.XMCPHeader, &headerName); err != nil || headerName == "" {
				return fmt.Errorf("property %q: x-mcp-header must be a non-empty string", path)
			}
			if err := validateHeaderToken(headerName); err != nil {
				return fmt.Errorf("property %q: %w", path, err)
			}
			lower := strings.ToLower(headerName)
			if seen[lower] {
				return fmt.Errorf(
					"property %q: duplicate x-mcp-header value %q (case-insensitive)", path, headerName,
				)
			}
			seen[lower] = true
		}
		if len(prop.Properties) > 0 {
			if err := validateParamHeadersIn(prop.Properties, path, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateHeaderToken checks name against the HTTP field-name token syntax
// (1*tchar, RFC 9110 §5.6.2).
func validateHeaderToken(name string) error {
	for _, c := range name {
		if !isHeaderTChar(c) {
			return fmt.Errorf("x-mcp-header value %q contains invalid character %q", name, c)
		}
	}
	return nil
}

func isHeaderTChar(c rune) bool {
	switch {
	case c >= '0' && c <= '9', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
