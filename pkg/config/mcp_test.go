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

// reverseValidationContext returns a context carrying an "oauth" identity
// profile, matching baseValidReverseServer's Identity reference. The default
// edge.pairing.type (remote) requires reverse servers to reference a known
// identity profile, so tests that aren't specifically exercising that
// requirement need one in context to isolate the behavior they do target.
func reverseValidationContext(t *testing.T) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), identitiesContextKey{}, map[string]*IdentityProfile{
		"oauth": {
			Source:  IdentitySourceJWT,
			Issuer:  "https://idp.example.com",
			JWKSURL: "https://idp.example.com/.well-known/jwks.json",
		},
	})
}

func TestServer_ValidateWithContext_Reverse_Valid(t *testing.T) {
	s := baseValidReverseServer()
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_Reverse_RequiresOrigin(t *testing.T) {
	s := baseValidReverseServer()
	s.Origin = ""
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsOriginWithPath(t *testing.T) {
	s := baseValidReverseServer()
	s.Origin = "https://app1.example.com/tools"
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_DefaultIsRemote_RequiresIdentity(t *testing.T) {
	// エッジ設定なしの ctx はデフォルト (pairing/remote) 相当として扱われるため、
	// identity 未指定はエラーになる。
	s := baseValidReverseServer()
	s.Identity = ""
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_StaticPairing_NoIdentityRequired(t *testing.T) {
	s := baseValidReverseServer()
	s.Identity = ""
	ctx := context.WithValue(
		t.Context(),
		edgeContextKey{},
		EdgeConfig{Pairing: PairingConfig{Type: PairingTypeStatic}},
	)
	err := s.ValidateWithContext(ctx)
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

func TestServer_ValidateWithContext_Reverse_RemotePairing_UnknownIdentity_Invalid(t *testing.T) {
	s := baseValidReverseServer()
	ctx := context.WithValue(
		t.Context(),
		edgeContextKey{},
		EdgeConfig{Pairing: PairingConfig{Type: PairingTypeRemote}},
	)
	// identitiesContextKey が未設定（identities なし）の場合も、"oauth" は存在しない
	// プロファイルとして扱われエラーになる。
	err := s.ValidateWithContext(ctx)
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RemotePairing_KnownIdentity_Valid(t *testing.T) {
	s := baseValidReverseServer()
	ctx := context.WithValue(
		t.Context(),
		edgeContextKey{},
		EdgeConfig{Pairing: PairingConfig{Type: PairingTypeRemote}},
	)
	ctx = context.WithValue(ctx, identitiesContextKey{}, map[string]*IdentityProfile{
		"oauth": {
			Source:  IdentitySourceJWT,
			Issuer:  "https://idp.example.com",
			JWKSURL: "https://idp.example.com/.well-known/jwks.json",
		},
	})
	err := s.ValidateWithContext(ctx)
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsAuthValue(t *testing.T) {
	s := baseValidReverseServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	err := s.ValidateWithContext(reverseValidationContext(t))
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
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsTokenExchange(t *testing.T) {
	s := baseValidReverseServer()
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsCommand(t *testing.T) {
	s := baseValidReverseServer()
	s.Command = "/bin/server"
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsURL(t *testing.T) {
	s := baseValidReverseServer()
	s.URL = "http://example.com"
	err := s.ValidateWithContext(reverseValidationContext(t))
	require.Error(t, err)
}

func TestServer_ValidateWithContext_Reverse_RejectsSpec(t *testing.T) {
	// spec (OpenAPI mode) and transport: reverse must not both be set — the
	// server would otherwise be ambiguous between the two backend kinds.
	s := baseValidReverseServer()
	s.Spec = "openapi.yaml"
	s.BaseURL = "https://api.example.com"
	err := s.ValidateWithContext(reverseValidationContext(t))
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

func TestServer_EffectiveSpecRefreshInterval(t *testing.T) {
	ptr := func(d time.Duration) *time.Duration { return &d }

	tests := []struct {
		name     string
		server   Server
		global   time.Duration
		expected time.Duration
	}{
		{
			name:     "未設定はグローバル既定を使う",
			server:   Server{Spec: "openapi.yaml"},
			global:   5 * time.Minute,
			expected: 5 * time.Minute,
		},
		{
			name:     "0 はサーバー単位で無効化する",
			server:   Server{Spec: "openapi.yaml", SpecRefreshInterval: ptr(0)},
			global:   5 * time.Minute,
			expected: 0,
		},
		{
			name:     "正値はグローバル既定より優先される",
			server:   Server{Spec: "openapi.yaml", SpecRefreshInterval: ptr(30 * time.Second)},
			global:   5 * time.Minute,
			expected: 30 * time.Second,
		},
		{
			name:     "グローバル既定も未設定なら無効",
			server:   Server{Spec: "openapi.yaml"},
			global:   0,
			expected: 0,
		},
		{
			name:     "MCP バックエンドモードは対象外",
			server:   Server{Transport: MCPTransportHTTP, URL: "http://example.com"},
			global:   5 * time.Minute,
			expected: 0,
		},
		{
			name: "reverse も対象外",
			server: Server{
				Transport:           MCPTransportReverse,
				Origin:              "https://app.example.com",
				SpecRefreshInterval: ptr(30 * time.Second),
			},
			global:   5 * time.Minute,
			expected: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.server.EffectiveSpecRefreshInterval(tt.global))
		})
	}
}

func TestServer_ValidateWithContext_NegativeSpecRefreshInterval_Invalid(t *testing.T) {
	s := baseValidServer()
	d := -1 * time.Second
	s.SpecRefreshInterval = &d
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "SpecRefreshInterval")
}

func TestServer_ValidateWithContext_ZeroSpecRefreshInterval_Valid(t *testing.T) {
	s := baseValidServer()
	d := time.Duration(0)
	s.SpecRefreshInterval = &d
	require.NoError(t, s.ValidateWithContext(t.Context()))
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

// --- Server.Tools / GeneratedToolsFile / EffectiveSpecRefreshInterval ---

func baseValidOpenAPIServer() Server {
	return Server{
		Description: "test openapi server",
		Spec:        "http://example.com/openapi.json",
		BaseURL:     "http://example.com",
	}
}

func TestServer_GeneratedToolsFile_Unset(t *testing.T) {
	s := Server{}
	require.Equal(t, "", s.GeneratedToolsFile())
}

func TestServer_GeneratedToolsFile_Set(t *testing.T) {
	s := Server{Tools: &ToolsConfig{File: "./generated/petstore.yaml"}}
	require.Equal(t, "./generated/petstore.yaml", s.GeneratedToolsFile())
}

func TestServer_EffectiveSpecRefreshInterval_ToolsFileOverridesGlobal(t *testing.T) {
	s := Server{
		Spec:  "http://example.com/openapi.json",
		Tools: &ToolsConfig{File: "./generated/petstore.yaml"},
	}
	require.Equal(t, time.Duration(0), s.EffectiveSpecRefreshInterval(5*time.Minute))
}

func TestServer_ValidateWithContext_ToolsFile_Valid(t *testing.T) {
	s := baseValidOpenAPIServer()
	s.Tools = &ToolsConfig{File: "./generated/petstore.yaml"}
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_ToolsFile_RequiresSpec(t *testing.T) {
	s := Server{
		Description: "mcp backend",
		Transport:   MCPTransportHTTP,
		URL:         "http://example.com",
		Tools:       &ToolsConfig{File: "./generated/backend.yaml"},
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "tools.file requires spec to be set")
}

func TestServer_ValidateWithContext_ToolsFile_RejectsReverseServer(t *testing.T) {
	s := Server{
		Description: "reverse server",
		Transport:   MCPTransportReverse,
		Origin:      "https://app1.example.com",
		Tools:       &ToolsConfig{File: "./generated/reverse.yaml"},
	}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "tools.file requires spec to be set")
}

func TestServer_ValidateWithContext_ToolsFile_RejectsHTTPURL(t *testing.T) {
	s := baseValidOpenAPIServer()
	s.Tools = &ToolsConfig{File: "https://example.com/generated/petstore.yaml"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "tools.file must be a local path")
}

func TestServer_ValidateWithContext_ToolsFile_RejectsHTTPSURL(t *testing.T) {
	s := baseValidOpenAPIServer()
	s.Tools = &ToolsConfig{File: "http://example.com/generated/petstore.yaml"}
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "tools.file must be a local path")
}

func TestServer_ValidateWithContext_ToolsFile_ExclusiveWithSpecRefreshInterval(t *testing.T) {
	s := baseValidOpenAPIServer()
	s.Tools = &ToolsConfig{File: "./generated/petstore.yaml"}
	interval := 5 * time.Minute
	s.SpecRefreshInterval = &interval
	err := s.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestServer_ValidateWithContext_ToolsFile_ZeroSpecRefreshIntervalOK(t *testing.T) {
	s := baseValidOpenAPIServer()
	s.Tools = &ToolsConfig{File: "./generated/petstore.yaml"}
	zero := time.Duration(0)
	s.SpecRefreshInterval = &zero
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestServer_ValidateWithContext_ToolsUnset_NoError(t *testing.T) {
	s := baseValidOpenAPIServer()
	err := s.ValidateWithContext(t.Context())
	require.NoError(t, err)
}
