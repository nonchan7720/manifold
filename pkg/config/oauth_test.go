package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- CIMDConfig.WithDefaults ---

func TestCIMDConfig_WithDefaults(t *testing.T) {
	got := CIMDConfig{}.WithDefaults()
	require.Equal(t, DefaultCIMDCacheTTL, got.CacheTTL)
	require.Equal(t, DefaultCIMDMaxDocumentSize, got.MaxDocumentSize)
	require.False(t, got.Enabled)
}

func TestCIMDConfig_WithDefaults_KeepsExplicitValues(t *testing.T) {
	got := CIMDConfig{
		Enabled:         true,
		CacheTTL:        5 * time.Minute,
		MaxDocumentSize: 1024,
	}.WithDefaults()
	require.Equal(t, 5*time.Minute, got.CacheTTL)
	require.EqualValues(t, 1024, got.MaxDocumentSize)
}

// --- CIMDConfig.AllowsOrigin ---

func TestCIMDConfig_AllowsOrigin_EmptyListAllowsAll(t *testing.T) {
	c := CIMDConfig{Enabled: true}
	require.True(t, c.AllowsOrigin("https://client.example.com"))
	require.True(t, c.AllowsOrigin("https://other.example.org"))
}

func TestCIMDConfig_AllowsOrigin_ListRestricts(t *testing.T) {
	c := CIMDConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://client.example.com"},
	}
	require.True(t, c.AllowsOrigin("https://client.example.com"))
	require.False(t, c.AllowsOrigin("https://evil.example.com"))
}

func TestCIMDConfig_AllowsOrigin_TrailingSlashAndCaseInsensitiveHost(t *testing.T) {
	c := CIMDConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://Client.Example.com/"},
	}
	require.True(t, c.AllowsOrigin("https://client.example.com"))
}

// --- CIMDConfig.ValidateWithContext ---

func TestCIMDConfig_ValidateWithContext_DisabledSkipsChecks(t *testing.T) {
	c := CIMDConfig{
		Enabled:         false,
		AllowedOrigins:  []string{"not a url"},
		CacheTTL:        -time.Second,
		MaxDocumentSize: -1,
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestCIMDConfig_ValidateWithContext_EnabledDefaults(t *testing.T) {
	c := CIMDConfig{Enabled: true}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestCIMDConfig_ValidateWithContext_NegativeCacheTTL(t *testing.T) {
	c := CIMDConfig{Enabled: true, CacheTTL: -time.Second}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestCIMDConfig_ValidateWithContext_ZeroValuesUseDefaults(t *testing.T) {
	c := CIMDConfig{Enabled: true, CacheTTL: 0, MaxDocumentSize: 0}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestCIMDConfig_ValidateWithContext_NegativeMaxDocumentSize(t *testing.T) {
	c := CIMDConfig{Enabled: true, MaxDocumentSize: -1}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestCIMDConfig_ValidateWithContext_AllowedOrigins(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		wantErr bool
	}{
		{name: "https origin", origin: "https://client.example.com", wantErr: false},
		{name: "origin with port", origin: "https://client.example.com:8443", wantErr: false},
		{name: "trailing slash", origin: "https://client.example.com/", wantErr: false},
		{name: "with path", origin: "https://client.example.com/oauth", wantErr: true},
		{name: "no scheme", origin: "client.example.com", wantErr: true},
		{name: "empty", origin: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CIMDConfig{Enabled: true, AllowedOrigins: []string{tt.origin}}
			err := c.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// --- OAuthConfig ---

func TestOAuthConfig_ValidateWithContext_PropagatesCIMD(t *testing.T) {
	c := OAuthConfig{CIMD: CIMDConfig{Enabled: true, MaxDocumentSize: -1}}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestOAuthConfig_ValidateWithContext_Zero(t *testing.T) {
	require.NoError(t, OAuthConfig{}.ValidateWithContext(t.Context()))
}
