package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsMCPBackend(t *testing.T) {
	tests := []struct {
		name     string
		server   Server
		expected bool
	}{
		{
			name:     "OpenAPI mode: spec set, no transport",
			server:   Server{Spec: "http://example.com/openapi.json"},
			expected: false,
		},
		{
			name:     "MCP backend: HTTP transport, no spec",
			server:   Server{Transport: MCPTransportHTTP, URL: "http://example.com"},
			expected: true,
		},
		{
			name:     "MCP backend: stdio transport, no spec",
			server:   Server{Transport: MCPTransportStdio, Command: "/bin/server"},
			expected: true,
		},
		{
			name:     "Both spec and transport: spec takes precedence (not MCP backend)",
			server:   Server{Spec: "http://example.com/openapi.json", Transport: MCPTransportHTTP},
			expected: false,
		},
		{
			name:     "Neither spec nor transport",
			server:   Server{},
			expected: false,
		},
		{
			name:     "Transport set but spec also set",
			server:   Server{Spec: "local/spec.json", Transport: MCPTransportStdio},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.server.IsMCPBackend()
			require.Equal(t, tt.expected, got)
		})
	}
}
