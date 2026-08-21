package config

import (
	"context"
	"testing"
	"time"

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

// --- Server.ValidateWithContext: reverse transport ---

func baseValidReverseServer() Server {
	return Server{
		Description: "reverse server",
		Transport:   MCPTransportReverse,
		Origin:      "https://app1.example.com",
		Identity:    "oauth",
	}
}

func TestServer_ValidateWithContext_Reverse_Valid(t *testing.T) {
	s := baseValidReverseServer()
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_Reverse_RequiresOrigin(t *testing.T) {
	s := baseValidReverseServer()
	s.Origin = ""
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsOriginWithPath(t *testing.T) {
	s := baseValidReverseServer()
	s.Origin = "https://app1.example.com/tools"
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_NoIdentityRequired_NoEdgeConfig(t *testing.T) {
	// エッジ設定なしの ctx はデフォルト (pairing/static) 相当として扱われるため、
	// identity 未指定でもエラーにならない。
	s := baseValidReverseServer()
	s.Identity = ""
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_Reverse_RemotePairing_RequiresIdentity(t *testing.T) {
	s := baseValidReverseServer()
	s.Identity = ""
	ctx := context.WithValue(
		t.Context(),
		edgeContextKey{},
		EdgeConfig{Pairing: PairingConfig{Type: PairingTypeRemote}},
	)
	err := s.ValidateWithContext(ctx)
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsAuthValue(t *testing.T) {
	s := baseValidReverseServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsOAuth2(t *testing.T) {
	s := baseValidReverseServer()
	s.OAuth2 = &OAuth2{
		ClientID:     "id",
		ClientSecret: "secret",
		AuthURL:      "https://example.com/auth",
		TokenURL:     "https://example.com/token",
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsTokenExchange(t *testing.T) {
	s := baseValidReverseServer()
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsCommand(t *testing.T) {
	s := baseValidReverseServer()
	s.Command = "/bin/server"
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsURL(t *testing.T) {
	s := baseValidReverseServer()
	s.URL = "http://example.com"
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsSpec(t *testing.T) {
	// spec (OpenAPI mode) and transport: reverse must not both be set — the
	// server would otherwise be ambiguous between the two backend kinds.
	s := baseValidReverseServer()
	s.Spec = "openapi.yaml"
	s.BaseURL = "https://api.example.com"
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_CallTimeoutOrDefault_UsesDefaultWhenUnset(t *testing.T) {
	s := baseValidReverseServer()
	require.Equal(t, DefaultCallTimeout, s.CallTimeoutOrDefault())
}

func TestServer_CallTimeoutOrDefault_UsesConfiguredValue(t *testing.T) {
	s := baseValidReverseServer()
	s.CallTimeout = 30 * time.Second
	require.Equal(t, 30*time.Second, s.CallTimeoutOrDefault())
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
		{
			name: "Reverse transport is not an MCP backend (handled by the edge registry)",
			server: Server{
				Transport: MCPTransportReverse,
				Origin:    "https://app1.example.com",
			},
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

func TestIsReverseBackend(t *testing.T) {
	tests := []struct {
		name     string
		server   Server
		expected bool
	}{
		{
			name:     "reverse transport",
			server:   Server{Transport: MCPTransportReverse, Origin: "https://app1.example.com"},
			expected: true,
		},
		{
			name:     "http backend",
			server:   Server{Transport: MCPTransportHTTP, URL: "http://example.com"},
			expected: false,
		},
		{
			name:     "openapi mode",
			server:   Server{Spec: "http://example.com/openapi.json"},
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.server.IsReverseBackend())
		})
	}
}
