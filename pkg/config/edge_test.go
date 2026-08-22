package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- NormalizeOrigin ---

func TestNormalizeOrigin_AcceptsSchemeAndHost(t *testing.T) {
	got, err := NormalizeOrigin("https://app1.example.com")
	require.NoError(t, err)
	require.Equal(t, "https://app1.example.com", got)
}

func TestNormalizeOrigin_LowercasesSchemeAndHost(t *testing.T) {
	got, err := NormalizeOrigin("HTTPS://App1.Example.COM")
	require.NoError(t, err)
	require.Equal(t, "https://app1.example.com", got)
}

func TestNormalizeOrigin_KeepsExplicitPort(t *testing.T) {
	got, err := NormalizeOrigin("http://localhost:5173")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:5173", got)
}

func TestNormalizeOrigin_TrimsTrailingSlash(t *testing.T) {
	got, err := NormalizeOrigin("https://app1.example.com/")
	require.NoError(t, err)
	require.Equal(t, "https://app1.example.com", got)
}

func TestNormalizeOrigin_RejectsPath(t *testing.T) {
	_, err := NormalizeOrigin("https://app1.example.com/tools")
	require.Error(t, err)
}

func TestNormalizeOrigin_RejectsQuery(t *testing.T) {
	_, err := NormalizeOrigin("https://app1.example.com?x=1")
	require.Error(t, err)
}

func TestNormalizeOrigin_RejectsMissingScheme(t *testing.T) {
	_, err := NormalizeOrigin("app1.example.com")
	require.Error(t, err)
}

func TestNormalizeOrigin_RejectsUnsupportedScheme(t *testing.T) {
	_, err := NormalizeOrigin("ftp://app1.example.com")
	require.Error(t, err)
}

func TestNormalizeOrigin_RejectsEmpty(t *testing.T) {
	_, err := NormalizeOrigin("")
	require.Error(t, err)
}

func TestNormalizeOrigin_StripsDefaultHTTPSPort(t *testing.T) {
	got, err := NormalizeOrigin("https://app1.example.com:443")
	require.NoError(t, err)
	require.Equal(t, "https://app1.example.com", got)
}

func TestNormalizeOrigin_StripsDefaultHTTPPort(t *testing.T) {
	got, err := NormalizeOrigin("http://app1.example.com:80")
	require.NoError(t, err)
	require.Equal(t, "http://app1.example.com", got)
}

func TestNormalizeOrigin_KeepsNonDefaultPortForScheme(t *testing.T) {
	got, err := NormalizeOrigin("https://app1.example.com:8443")
	require.NoError(t, err)
	require.Equal(t, "https://app1.example.com:8443", got)
}

func TestNormalizeOrigin_StripsZeroPaddedDefaultHTTPSPort(t *testing.T) {
	got, err := NormalizeOrigin("https://app1.example.com:0443")
	require.NoError(t, err)
	require.Equal(t, "https://app1.example.com", got)
}

func TestNormalizeOrigin_StripsZeroPaddedDefaultHTTPPort(t *testing.T) {
	got, err := NormalizeOrigin("http://app1.example.com:00080")
	require.NoError(t, err)
	require.Equal(t, "http://app1.example.com", got)
}

func TestNormalizeOrigin_KeepsIPv6HostBrackets(t *testing.T) {
	got, err := NormalizeOrigin("https://[2001:db8::1]:443")
	require.NoError(t, err)
	require.Equal(t, "https://[2001:db8::1]", got)
}

func TestNormalizeOrigin_KeepsIPv6HostWithNonDefaultPort(t *testing.T) {
	got, err := NormalizeOrigin("https://[2001:db8::1]:8443")
	require.NoError(t, err)
	require.Equal(t, "https://[2001:db8::1]:8443", got)
}

// --- EdgeConfig.WithDefaults ---

func TestEdgeConfig_WithDefaults_FillsPairingRemote(t *testing.T) {
	// 正本 docs/design/webmcp-reverse-gateway.ja.md の「拡張と identity の
	// 紐づけ」表のとおり、既定は pairing + type: remote。
	got := EdgeConfig{}.WithDefaults()
	require.Equal(t, EdgeAuthPairing, got.Auth)
	require.Equal(t, PairingTypeRemote, got.Pairing.Type)
}

func TestEdgeConfig_WithDefaults_KeepsExplicitValues(t *testing.T) {
	got := EdgeConfig{
		Auth:    EdgeAuthForwardAuth,
		Pairing: PairingConfig{Type: PairingTypeStatic},
	}.WithDefaults()
	require.Equal(t, EdgeAuthForwardAuth, got.Auth)
	require.Equal(t, PairingTypeStatic, got.Pairing.Type)
}

// --- EdgeConfig.ValidateWithContext ---

func TestEdgeConfig_ValidateWithContext_DefaultsValid(t *testing.T) {
	c := EdgeConfig{}.WithDefaults()
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestEdgeConfig_ValidateWithContext_RejectsUnknownAuth(t *testing.T) {
	c := EdgeConfig{Auth: "bogus"}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestEdgeConfig_ValidateWithContext_RejectsUnknownPairingType(t *testing.T) {
	c := EdgeConfig{Auth: EdgeAuthPairing, Pairing: PairingConfig{Type: "bogus"}}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestEdgeConfig_ValidateWithContext_RejectsForwardAuth(t *testing.T) {
	// forwardAuth is config structure only (not implemented yet); see the
	// PR's "Known limitations". Accepting it here would let
	// mcpAuthMiddleware's static-pairing JWT skip apply to a deployment that
	// believes it's protected by a front-door auth proxy.
	c := EdgeConfig{Auth: EdgeAuthForwardAuth, Pairing: PairingConfig{Type: PairingTypeStatic}}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestEdgeConfig_ValidateWithContext_AcceptsRemotePairing(t *testing.T) {
	// remote pairing is implemented in Phase 2a (see
	// docs/design/webmcp-reverse-gateway-phase2.ja.md).
	c := EdgeConfig{Auth: EdgeAuthPairing, Pairing: PairingConfig{Type: PairingTypeRemote}}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestEdgeConfig_ValidateWithContext_AcceptsStaticPairing(t *testing.T) {
	c := EdgeConfig{Auth: EdgeAuthPairing, Pairing: PairingConfig{Type: PairingTypeStatic}}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}
