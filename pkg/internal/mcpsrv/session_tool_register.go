package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SessionResolver resolves the *mcp.ClientSession that should currently serve
// tools/call for a registered tool. Separating "which tools are registered"
// (from a past tools/list) from "which live session serves them" lets the
// backing session change (reconnect, tab reload) without re-registering
// tools, unlike MCPBackendClient.registerTools' closure-over-session
// structure.
type SessionResolver func(ctx context.Context) (*mcp.ClientSession, error)

// RegisterSessionTools registers each of tools on srv, forwarding tools/call
// to whatever session resolve returns at call time. If resolve fails (e.g. no
// live backend), the call returns a tool error carrying resolve's message
// rather than failing the RPC itself.
//
// tools come from a tab's tools/list, i.e. from an untrusted peer. A
// definition that (*mcp.Server).AddTool would panic on (no inputSchema, an
// inputSchema whose type is not "object", an unmarshalable outputSchema) is
// skipped with a warning instead of taking the whole gateway process down
// (AddTool runs outside any HTTP handler, so middleware.Recover never sees
// it). The remaining tools of the same tab are still registered.
func RegisterSessionTools(
	ctx context.Context,
	srv *mcp.Server,
	tools []*mcp.Tool,
	resolve SessionResolver,
) {
	for _, tool := range tools {
		t := tool
		if err := validateSessionTool(t); err != nil {
			name := ""
			if t != nil {
				name = t.Name
			}
			slog.WarnContext(ctx, "skipping tab tool with an invalid definition",
				slog.String("tool", name), slog.String("error", err.Error()))
			continue
		}
		srv.AddTool(
			t,
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				session, err := resolve(ctx)
				if err != nil {
					result := &mcp.CallToolResult{}
					result.SetError(err)
					return result, nil
				}
				return session.CallTool(ctx, &mcp.CallToolParams{
					Name:      req.Params.Name,
					Arguments: req.Params.Arguments,
				})
			},
		)
	}
}

// validateSessionTool mirrors the checks (*mcp.Server).AddTool panics on, so a
// peer-supplied tool definition can be rejected before it reaches AddTool.
// Both schema fields are compared after a JSON round trip, which covers every
// representation the SDK accepts (map[string]any, json.RawMessage,
// *jsonschema.Schema, ...).
func validateSessionTool(t *mcp.Tool) error {
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
