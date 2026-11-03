package mcpsrv

import (
	"context"
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
//
// 検査と登録は addToolSafely が行う（x-mcp-header 注釈の検査と recover を含む）。
func RegisterSessionTools(
	ctx context.Context,
	srv *mcp.Server,
	tools []*mcp.Tool,
	resolve SessionResolver,
) {
	for _, tool := range tools {
		t := tool
		err := addToolSafely(
			srv,
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
		if err != nil {
			name := ""
			if t != nil {
				name = t.Name
			}
			slog.WarnContext(ctx, "skipping tab tool with an invalid definition",
				slog.String("tool", name), slog.String("error", err.Error()))
		}
	}
}
