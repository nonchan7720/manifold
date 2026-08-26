package authz

import "context"

// ToolRef identifies a single MCP tool by its owning server and tool name.
type ToolRef struct {
	Server string
	Name   string
}

// Decider is the PEP-facing interface for a tool-authorization PDP.
type Decider interface {
	// Allow reports whether p may call t.
	Allow(ctx context.Context, p Principal, t ToolRef) (bool, error)

	// AllowedTools filters tools down to the subset p may call, preserving
	// the input order.
	AllowedTools(ctx context.Context, p Principal, tools []ToolRef) ([]ToolRef, error)
}
