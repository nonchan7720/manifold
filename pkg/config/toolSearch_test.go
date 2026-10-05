package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolSearchConfig_WithDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   ToolSearchConfig
		want ToolSearchConfig
	}{
		{
			name: "zero value gets defaults",
			in:   ToolSearchConfig{},
			want: ToolSearchConfig{
				Threshold:      DefaultToolSearchThreshold,
				DefaultLimit:   DefaultToolSearchLimit,
				ResultFormat:   ToolSearchResultFormatDefault,
				DigestMaxTools: DefaultToolSearchDigestMaxTools,
			},
		},
		{
			name: "negative gets defaults",
			in:   ToolSearchConfig{Threshold: -1, DefaultLimit: -1},
			want: ToolSearchConfig{
				Threshold:      DefaultToolSearchThreshold,
				DefaultLimit:   DefaultToolSearchLimit,
				ResultFormat:   ToolSearchResultFormatDefault,
				DigestMaxTools: DefaultToolSearchDigestMaxTools,
			},
		},
		{
			name: "digestMaxTools -1 (all) is kept, not replaced by the default",
			in:   ToolSearchConfig{DigestMaxTools: -1},
			want: ToolSearchConfig{
				Threshold:      DefaultToolSearchThreshold,
				DefaultLimit:   DefaultToolSearchLimit,
				ResultFormat:   ToolSearchResultFormatDefault,
				DigestMaxTools: -1,
			},
		},
		{
			name: "explicit values kept",
			in: ToolSearchConfig{
				Enabled: true, Threshold: 5, DefaultLimit: 3,
				ResultFormat: ToolSearchResultFormatClaude, DigestMaxTools: 20,
			},
			want: ToolSearchConfig{
				Enabled: true, Threshold: 5, DefaultLimit: 3,
				ResultFormat: ToolSearchResultFormatClaude, DigestMaxTools: 20,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.in.WithDefaults())
		})
	}
}

func TestToolSearchConfig_IsEnabled(t *testing.T) {
	require.False(t, ToolSearchConfig{}.IsEnabled(), "unset means off")
	require.False(t, ToolSearchConfig{}.WithDefaults().IsEnabled(), "WithDefaults keeps it off")
	require.True(t, ToolSearchConfig{Enabled: true}.IsEnabled())
	require.True(t, ToolSearchConfig{Enabled: true}.WithDefaults().IsEnabled())
}

func TestToolSearchConfig_ValidateWithContext(t *testing.T) {
	tests := []struct {
		name    string
		in      ToolSearchConfig
		wantErr bool
	}{
		{"zero value valid", ToolSearchConfig{}, false},
		{"defaults valid", ToolSearchConfig{}.WithDefaults(), false},
		{"enabled valid", ToolSearchConfig{Enabled: true}, false},
		{"negative threshold invalid", ToolSearchConfig{Threshold: -1}, true},
		{"negative default limit invalid", ToolSearchConfig{DefaultLimit: -1}, true},
		{
			"default result format valid",
			ToolSearchConfig{ResultFormat: ToolSearchResultFormatDefault},
			false,
		},
		{
			"claude result format valid",
			ToolSearchConfig{ResultFormat: ToolSearchResultFormatClaude},
			false,
		},
		{"unknown result format invalid", ToolSearchConfig{ResultFormat: "bogus"}, true},
		{"digestMaxTools -1 valid", ToolSearchConfig{DigestMaxTools: -1}, false},
		{"digestMaxTools positive valid", ToolSearchConfig{DigestMaxTools: 1}, false},
		{"digestMaxTools -2 invalid", ToolSearchConfig{DigestMaxTools: -2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestConfig_ValidateWithContext_ToolSearch(t *testing.T) {
	cfg := newValidConfigWithServers(Servers{})
	cfg.Gateway.ToolSearch = ToolSearchConfig{Threshold: -1}
	require.Error(t, cfg.ValidateWithContext(t.Context()))

	cfg.Gateway.ToolSearch = ToolSearchConfig{Threshold: 5}
	require.NoError(t, cfg.ValidateWithContext(t.Context()))
}

const toolSearchTestYAML = `
mcpServers:
  petstore:
    description: Swagger Petstore
    spec: https://petstore3.swagger.io/api/v3/openapi.json
`

func TestLoadInternal_ToolSearch_Defaults(t *testing.T) {
	cfg, err := loadFromYAML(t, toolSearchTestYAML)
	require.NoError(t, err)
	require.Equal(t, ToolSearchConfig{}.WithDefaults(), cfg.Gateway.ToolSearch)
}

func TestLoadInternal_ToolSearch_FromFile(t *testing.T) {
	cfg, err := loadFromYAML(t, `
gateway:
  toolSearch:
    enabled: true
    threshold: 5
    defaultLimit: 3
    resultFormat: claude
    digestMaxTools: 20
`+toolSearchTestYAML)
	require.NoError(t, err)
	require.Equal(t, ToolSearchConfig{
		Enabled:        true,
		Threshold:      5,
		DefaultLimit:   3,
		ResultFormat:   ToolSearchResultFormatClaude,
		DigestMaxTools: 20,
	}, cfg.Gateway.ToolSearch)
}

func TestLoadInternal_ToolSearch_DisabledByDefault(t *testing.T) {
	cfg, err := loadFromYAML(t, toolSearchTestYAML)
	require.NoError(t, err)
	require.False(t, cfg.Gateway.ToolSearch.IsEnabled())
	require.Equal(t, DefaultToolSearchThreshold, cfg.Gateway.ToolSearch.Threshold,
		"the other fields still take their defaults")

	cfg, err = loadFromYAML(t, `
gateway:
  toolSearch:
    enabled: true
`+toolSearchTestYAML)
	require.NoError(t, err)
	require.True(t, cfg.Gateway.ToolSearch.IsEnabled())

	t.Setenv("GATEWAY_TOOLSEARCH_ENABLED", "true")
	cfg, err = loadFromYAML(t, toolSearchTestYAML)
	require.NoError(t, err)
	require.True(t, cfg.Gateway.ToolSearch.IsEnabled(), "env override")
}

func TestLoadInternal_ToolSearch_EnvOverrides(t *testing.T) {
	t.Setenv("GATEWAY_TOOLSEARCH_ENABLED", "true")
	t.Setenv("GATEWAY_TOOLSEARCH_THRESHOLD", "5")
	t.Setenv("GATEWAY_TOOLSEARCH_DEFAULTLIMIT", "3")
	t.Setenv("GATEWAY_TOOLSEARCH_RESULTFORMAT", "claude")
	t.Setenv("GATEWAY_TOOLSEARCH_DIGESTMAXTOOLS", "20")
	cfg, err := loadFromYAML(t, toolSearchTestYAML)
	require.NoError(t, err)
	require.Equal(t, ToolSearchConfig{
		Enabled:        true,
		Threshold:      5,
		DefaultLimit:   3,
		ResultFormat:   ToolSearchResultFormatClaude,
		DigestMaxTools: 20,
	}, cfg.Gateway.ToolSearch)
}

func TestLoadInternal_ToolSearch_InvalidResultFormat(t *testing.T) {
	t.Setenv("GATEWAY_TOOLSEARCH_RESULTFORMAT", "bogus")
	_, err := loadFromYAML(t, toolSearchTestYAML)
	require.Error(t, err)
}

func TestDefaultToolSearchDigestMaxTools_IsFinite(t *testing.T) {
	require.Equal(t, 50, DefaultToolSearchDigestMaxTools)
	require.Equal(t, 50, ToolSearchConfig{}.WithDefaults().DigestMaxTools)
}
