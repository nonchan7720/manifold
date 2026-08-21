package mcpsrv

import (
	"context"

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
func RegisterSessionTools(srv *mcp.Server, tools []*mcp.Tool, resolve SessionResolver) {
	for _, tool := range tools {
		t := tool
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
