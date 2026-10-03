package config

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// DefaultAggregateSeparator separates the server name from the tool name in
// the aggregated endpoint's tool names (<server>__<tool>).
const DefaultAggregateSeparator = "__"

// AggregateConfig exposes several servers' tools behind the single /mcp
// endpoint, each tool renamed <server><separator><tool>.
type AggregateConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Servers はまとめるサーバー名。空なら対象にできる全サーバー
	// （AggregatableServer 参照）。
	Servers []string `mapstructure:"servers"`
	// Separator はサーバー名とツール名の区切り。未設定は DefaultAggregateSeparator。
	Separator string `mapstructure:"separator"`
	// ToolSearch は集約エンドポイントの tools/list を search_tools / call_tool に
	// 置き換える（ServerのtoolSearchと同じ）。
	ToolSearch *ToolSearchConfig `mapstructure:"toolSearch"`
}

// SeparatorOrDefault returns Separator, falling back to
// DefaultAggregateSeparator when unset.
func (c AggregateConfig) SeparatorOrDefault() string {
	if c.Separator == "" {
		return DefaultAggregateSeparator
	}
	return c.Separator
}

// AggregatableServer reports why srv can't be part of the aggregated
// endpoint, or "" when it can. Reverse servers only exist per browser
// connection, and an oauth2 server needs a downstream OAuth flow of its own,
// which a single endpoint can't run for several servers at once.
func AggregatableServer(srv *Server) string {
	switch {
	case srv == nil:
		return "not defined"
	case srv.IsReverseBackend():
		return "reverse transport servers can't be aggregated"
	case srv.OAuth2 != nil:
		return "oauth2 servers need their own OAuth flow and can't be aggregated"
	default:
		return ""
	}
}

// Members returns the names of the servers the aggregated endpoint exposes,
// sorted: Servers when set, else every server AggregatableServer accepts.
func (c AggregateConfig) Members(servers Servers) []string {
	if len(c.Servers) > 0 {
		out := slices.Clone(c.Servers)
		slices.Sort(out)
		return slices.Compact(out)
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		if AggregatableServer(servers[name]) == "" {
			out = append(out, name)
		}
	}
	return out
}

// ValidateServers checks the aggregate against the merged server list
// (mcpServers plus agents), so it runs after mergeAgentsIntoServers.
func (c AggregateConfig) ValidateServers(ctx context.Context, servers Servers) error {
	if !c.Enabled {
		return nil
	}
	if c.ToolSearch != nil {
		if err := c.ToolSearch.ValidateWithContext(ctx); err != nil {
			return fmt.Errorf("gateway.aggregate.toolSearch: %w", err)
		}
	}
	sep := c.SeparatorOrDefault()
	if !toolNameRegex.MatchString(sep) {
		return fmt.Errorf(
			"gateway.aggregate.separator %q must contain only letters, digits, '_', '-' or '.'",
			sep,
		)
	}
	for _, name := range c.Servers {
		srv, ok := servers[name]
		if !ok {
			return fmt.Errorf("gateway.aggregate.servers: %q is not defined", name)
		}
		if reason := AggregatableServer(srv); reason != "" {
			return fmt.Errorf("gateway.aggregate.servers: %q: %s", name, reason)
		}
	}
	for _, name := range c.Members(servers) {
		if strings.Contains(name, sep) {
			return fmt.Errorf(
				"gateway.aggregate: server name %q must not contain the separator %q "+
					"(rename the server, set gateway.aggregate.separator, or leave it out of gateway.aggregate.servers)",
				name, sep,
			)
		}
	}
	return nil
}

func (c AggregateConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.ToolSearch),
	)
}
