package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolSearchConfig_WithDefaults(t *testing.T) {
	tests := []struct {
		name              string
		in                ToolSearchConfig
		wantThreshold     int
		wantLimit         int
		wantFormat        string
		wantDigestMaxTool int
	}{
		{"zero value gets defaults", ToolSearchConfig{}, DefaultToolSearchThreshold, DefaultToolSearchLimit, ToolSearchResultFormatDefault, DefaultToolSearchDigestMaxTools},
		{"negative gets defaults", ToolSearchConfig{Threshold: -1, DefaultLimit: -1}, DefaultToolSearchThreshold, DefaultToolSearchLimit, ToolSearchResultFormatDefault, DefaultToolSearchDigestMaxTools},
		{"explicit values kept", ToolSearchConfig{Threshold: 5, DefaultLimit: 3, ResultFormat: ToolSearchResultFormatClaude, DigestMaxTools: 20}, 5, 3, ToolSearchResultFormatClaude, 20},
		{"digestMaxTools -1 kept as-is", ToolSearchConfig{Threshold: 5, DefaultLimit: 3, DigestMaxTools: -1}, 5, 3, ToolSearchResultFormatDefault, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.in.WithDefaults()
			require.Equal(t, tt.wantThreshold, got.Threshold)
			require.Equal(t, tt.wantLimit, got.DefaultLimit)
			require.Equal(t, tt.wantFormat, got.ResultFormat)
			require.Equal(t, tt.wantDigestMaxTool, got.DigestMaxTools)
		})
	}
}

func TestToolSearchConfig_ValidateWithContext(t *testing.T) {
	tests := []struct {
		name    string
		in      ToolSearchConfig
		wantErr bool
	}{
		{"defaults valid", ToolSearchConfig{}.WithDefaults(), false},
		{"zero threshold valid (WithDefaults will replace it)", ToolSearchConfig{Threshold: 0, DefaultLimit: 10, DigestMaxTools: -1}, false},
		{"negative threshold invalid", ToolSearchConfig{Threshold: -1, DefaultLimit: 10, DigestMaxTools: -1}, true},
		{"negative default limit invalid", ToolSearchConfig{Threshold: 100, DefaultLimit: -1, DigestMaxTools: -1}, true},
		{"empty result format valid (not yet defaulted)", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, ResultFormat: "", DigestMaxTools: -1}, false},
		{"default result format valid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, ResultFormat: ToolSearchResultFormatDefault, DigestMaxTools: -1}, false},
		{"claude result format valid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, ResultFormat: ToolSearchResultFormatClaude, DigestMaxTools: -1}, false},
		{"unknown result format invalid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, ResultFormat: "bogus", DigestMaxTools: -1}, true},
		{"digestMaxTools -1 valid (all tools)", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, DigestMaxTools: -1}, false},
		{"digestMaxTools positive valid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, DigestMaxTools: 1}, false},
		{"digestMaxTools large positive valid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, DigestMaxTools: 1000}, false},
		{"digestMaxTools zero invalid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, DigestMaxTools: 0}, true},
		{"digestMaxTools -2 invalid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, DigestMaxTools: -2}, true},
		{"digestMaxTools large negative invalid", ToolSearchConfig{Threshold: 100, DefaultLimit: 10, DigestMaxTools: -100}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
