package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolScopeConfig_WithDefaults_FillsHeaders(t *testing.T) {
	got := ToolScopeConfig{}.WithDefaults()
	require.Equal(t, DefaultToolScopeHeaderServices, got.Headers.Services)
	require.Equal(t, DefaultToolScopeHeaderServers, got.Headers.Servers)
}

func TestToolScopeConfig_WithDefaults_KeepsExplicitValues(t *testing.T) {
	got := ToolScopeConfig{Headers: ToolScopeHeaders{
		Services: "x-acme-services",
		Servers:  "x-acme-servers",
	}}.WithDefaults()
	require.Equal(t, "x-acme-services", got.Headers.Services)
	require.Equal(t, "x-acme-servers", got.Headers.Servers)
}

func TestToolScopeConfig_ValidateWithContext(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ToolScopeConfig
		wantErr bool
	}{
		{"disabled", ToolScopeConfig{}, false},
		{
			"disabled ignores invalid values",
			ToolScopeConfig{Headers: ToolScopeHeaders{Services: "bad header"}},
			false,
		},
		{"enabled with defaults", ToolScopeConfig{Enabled: true}, false},
		{
			"enabled rejects invalid services header",
			ToolScopeConfig{Enabled: true, Headers: ToolScopeHeaders{Services: "bad header"}},
			true,
		},
		{
			"enabled rejects invalid servers header",
			ToolScopeConfig{Enabled: true, Headers: ToolScopeHeaders{Servers: "bad:header"}},
			true,
		},
		{
			"enabled rejects the same header for services and servers",
			ToolScopeConfig{Enabled: true, Headers: ToolScopeHeaders{
				Services: "x-scope", Servers: "X-Scope",
			}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
