package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- TokenExchange.ValidateWithContext ---

func TestTokenExchange_ValidateWithContext_RequiresURL(t *testing.T) {
	te := &TokenExchange{}
	err := te.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestTokenExchange_ValidateWithContext_RejectsSchemeLessPath(t *testing.T) {
	// "/token" のようなスキーム無しの値は is.RequestURI では許容されてしまっていたが、
	// is.RequestURL に変更したことで設定ロード時にエラーになるべき。
	te := &TokenExchange{URL: "/token"}
	err := te.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestTokenExchange_ValidateWithContext_AcceptsAbsoluteURL(t *testing.T) {
	te := &TokenExchange{URL: "https://example.com/token"}
	err := te.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

// --- Server.ValidateWithContext: AuthValue/OAuth2/TokenExchange の排他性 ---

func baseValidServer() Server {
	return Server{
		Description: "test server",
		Transport:   MCPTransportHTTP,
		URL:         "http://example.com",
	}
}

func TestServer_ValidateWithContext_NoAuthConfigured_Valid(t *testing.T) {
	s := baseValidServer()
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_OnlyAuthValue_Valid(t *testing.T) {
	s := baseValidServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_OnlyOAuth2_Valid(t *testing.T) {
	s := baseValidServer()
	s.OAuth2 = &OAuth2{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://example.com/auth",
		TokenURL:     "https://example.com/token",
	}
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_OnlyTokenExchange_Valid(t *testing.T) {
	s := baseValidServer()
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_AuthValueAndOAuth2_Invalid(t *testing.T) {
	s := baseValidServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	s.OAuth2 = &OAuth2{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://example.com/auth",
		TokenURL:     "https://example.com/token",
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_AuthValueAndTokenExchange_Invalid(t *testing.T) {
	s := baseValidServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_OAuth2AndTokenExchange_Invalid(t *testing.T) {
	s := baseValidServer()
	s.OAuth2 = &OAuth2{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://example.com/auth",
		TokenURL:     "https://example.com/token",
	}
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_AllThree_Invalid(t *testing.T) {
	s := baseValidServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	s.OAuth2 = &OAuth2{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://example.com/auth",
		TokenURL:     "https://example.com/token",
	}
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

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
