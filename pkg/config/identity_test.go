package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- IdentityProfile.ValidateWithContext: source ---

func TestIdentityProfile_ValidateWithContext_RequiresSource(t *testing.T) {
	p := &IdentityProfile{}
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_RejectsUnknownSource(t *testing.T) {
	p := &IdentityProfile{Source: IdentitySource("bogus")}
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

// --- IdentityProfile.ValidateWithContext: source: jwt ---

func validJWTProfile() *IdentityProfile {
	return &IdentityProfile{
		Source:  IdentitySourceJWT,
		Claim:   "sub",
		Issuer:  "https://idp.example.com",
		JWKSURL: "https://idp.example.com/.well-known/jwks.json",
	}
}

func TestIdentityProfile_ValidateWithContext_JWT_Valid(t *testing.T) {
	err := validJWTProfile().ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_AudienceOptional_Valid(t *testing.T) {
	p := validJWTProfile()
	p.Audience = "manifold"
	err := p.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RequiresIssuer(t *testing.T) {
	p := validJWTProfile()
	p.Issuer = ""
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RejectsRelativeIssuer(t *testing.T) {
	p := validJWTProfile()
	p.Issuer = "idp.example.com"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RequiresJWKSURL(t *testing.T) {
	p := validJWTProfile()
	p.JWKSURL = ""
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RejectsRelativeJWKSURL(t *testing.T) {
	p := validJWTProfile()
	p.JWKSURL = "/.well-known/jwks.json"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RejectsHeaderField(t *testing.T) {
	p := validJWTProfile()
	p.Header = "X-User-Id"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RejectsHashField(t *testing.T) {
	p := validJWTProfile()
	p.Hash = true
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_JWT_RejectsIntrospectionFields(t *testing.T) {
	p := validJWTProfile()
	p.URL = "https://agent-platform.example.com/introspect"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

// --- IdentityProfile.ValidateWithContext: source: header ---

func validHeaderProfile() *IdentityProfile {
	return &IdentityProfile{
		Source: IdentitySourceHeader,
		Header: "X-User-Id",
	}
}

func TestIdentityProfile_ValidateWithContext_Header_Valid(t *testing.T) {
	err := validHeaderProfile().ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestIdentityProfile_ValidateWithContext_Header_HashTrue_Valid(t *testing.T) {
	p := validHeaderProfile()
	p.Hash = true
	err := p.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestIdentityProfile_ValidateWithContext_Header_RequiresHeader(t *testing.T) {
	p := validHeaderProfile()
	p.Header = ""
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_Header_RejectsJWTFields(t *testing.T) {
	p := validHeaderProfile()
	p.Issuer = "https://idp.example.com"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_Header_RejectsIntrospectionFields(t *testing.T) {
	p := validHeaderProfile()
	p.CredentialHeader = "X-Api-Key"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

// --- IdentityProfile.ValidateWithContext: source: introspection ---

func validIntrospectionProfile() *IdentityProfile {
	return &IdentityProfile{
		Source:           IdentitySourceIntrospection,
		URL:              "https://agent-platform.example.com/introspect",
		CredentialHeader: "X-Api-Key",
	}
}

func TestIdentityProfile_ValidateWithContext_Introspection_Valid(t *testing.T) {
	err := validIntrospectionProfile().ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestIdentityProfile_ValidateWithContext_Introspection_CacheTTLOptional_Valid(t *testing.T) {
	p := validIntrospectionProfile()
	p.CacheTTL = 10 * time.Minute
	err := p.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

func TestIdentityProfile_ValidateWithContext_Introspection_RequiresURL(t *testing.T) {
	p := validIntrospectionProfile()
	p.URL = ""
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_Introspection_RejectsRelativeURL(t *testing.T) {
	p := validIntrospectionProfile()
	p.URL = "/introspect"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_Introspection_RequiresCredentialHeader(t *testing.T) {
	p := validIntrospectionProfile()
	p.CredentialHeader = ""
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_Introspection_RejectsJWTFields(t *testing.T) {
	p := validIntrospectionProfile()
	p.JWKSURL = "https://idp.example.com/.well-known/jwks.json"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestIdentityProfile_ValidateWithContext_Introspection_RejectsHeaderFields(t *testing.T) {
	p := validIntrospectionProfile()
	p.Header = "X-User-Id"
	err := p.ValidateWithContext(t.Context())
	require.Error(t, err)
}

// --- IdentityProfile.ClaimOrDefault / CacheTTLOrDefault ---

func TestIdentityProfile_ClaimOrDefault_DefaultsToSub(t *testing.T) {
	p := IdentityProfile{Source: IdentitySourceJWT}
	require.Equal(t, "sub", p.ClaimOrDefault())
}

func TestIdentityProfile_ClaimOrDefault_UsesConfiguredValue(t *testing.T) {
	p := IdentityProfile{Source: IdentitySourceJWT, Claim: "email"}
	require.Equal(t, "email", p.ClaimOrDefault())
}

func TestIdentityProfile_CacheTTLOrDefault_DefaultsToFiveMinutes(t *testing.T) {
	p := IdentityProfile{Source: IdentitySourceIntrospection}
	require.Equal(t, DefaultIntrospectionCacheTTL, p.CacheTTLOrDefault())
}

func TestIdentityProfile_CacheTTLOrDefault_UsesConfiguredValue(t *testing.T) {
	p := IdentityProfile{Source: IdentitySourceIntrospection, CacheTTL: 10 * time.Minute}
	require.Equal(t, 10*time.Minute, p.CacheTTLOrDefault())
}

// --- Config.Identities: auto-recursion through map elements ---

func TestConfig_ValidateWithContext_Identities_InvalidProfile_Invalid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{})
	cfg.Identities = map[string]*IdentityProfile{
		"broken": {Source: IdentitySourceJWT}, // missing issuer/jwksURL
	}
	err := cfg.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestConfig_ValidateWithContext_Identities_ValidProfile_Valid(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{})
	cfg.Identities = map[string]*IdentityProfile{
		"oauth": validJWTProfile(),
	}
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}

// --- Config.ValidateWithContext: Server.Identity reference integrity ---
//
// pairing.type: remote is still rejected by PairingConfig.ValidateWithContext
// in this PR (see docs/design/webmcp-reverse-gateway-phase2.ja.md, Phase 2a);
// the reference-integrity check itself is exercised directly against
// Server.ValidateWithContext in mcp_test.go, the same way the existing
// "identity is required unless static" check already is.

func TestConfig_ValidateWithContext_Reverse_StaticPairing_UnknownIdentity_StillValid(t *testing.T) {
	// static は identity を使わないため、未定義のプロファイル名を指していてもエラーにしない。
	cfg := newValidConfigWithServers(Servers{
		"app1": {
			Description: "app1",
			Transport:   MCPTransportReverse,
			Origin:      "https://app1.example.com",
			Identity:    "oauth",
		},
	})
	cfg.Gateway.Edge = EdgeConfig{Pairing: PairingConfig{Type: PairingTypeStatic}}
	err := cfg.ValidateWithContext(t.Context())
	require.NoError(t, err)
}
