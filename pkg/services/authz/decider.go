package authz

import "context"

// ToolRef identifies a single MCP tool by its owning server and tool name.
// Service is the server's service code (config.Server.ServiceCode), passed to
// the PDP alongside Server so a policy can grant a whole service at once.
type ToolRef struct {
	Server  string
	Service string
	Name    string
}

// serviceCode returns Service, falling back to Server when unset — the same
// default config.Server.ServiceCode applies to a server without a service.
func (t ToolRef) serviceCode() string {
	if t.Service == "" {
		return t.Server
	}
	return t.Service
}

// Decider is the PEP-facing interface for a tool-authorization PDP.
type Decider interface {
	// Allow reports whether p may call t.
	Allow(ctx context.Context, p Principal, t ToolRef) (bool, error)

	// AllowedTools filters tools down to the subset p may call, preserving
	// the input order.
	AllowedTools(ctx context.Context, p Principal, tools []ToolRef) ([]ToolRef, error)

	// AllowCatalog reports whether p may read the unfiltered tool catalog
	// (GET /mcp/list?tools=true).
	AllowCatalog(ctx context.Context, p Principal) (bool, error)
}
