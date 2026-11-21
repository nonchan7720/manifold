package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServer_ValidateWithContext_A2UI(t *testing.T) {
	openAPI := Server{
		Description: "test server",
		Spec:        "spec.json",
		BaseURL:     "https://api.example.com",
		A2UI:        &A2UIConfig{Forms: true, AssumeSupport: true},
	}
	require.NoError(t, openAPI.ValidateWithContext(t.Context()))
	require.True(t, openAPI.A2UIForms())

	// フォームは OpenAPI の入力スキーマから作るので、MCP バックエンドでは設定できない。
	mcpBackend := baseValidServer()
	mcpBackend.A2UI = &A2UIConfig{Forms: true}
	err := mcpBackend.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "is only supported in OpenAPI mode")

	var unset *Server
	require.False(t, unset.A2UIForms())
	require.False(t, (&Server{}).A2UIForms())
	require.False(t, (&Server{A2UI: &A2UIConfig{AssumeSupport: true}}).A2UIForms())
}

func TestServer_ValidateWithContext_A2UICatalog(t *testing.T) {
	server := func(a2ui *A2UIConfig) Server {
		return Server{
			Description: "test server",
			Spec:        "spec.json",
			BaseURL:     "https://api.example.com",
			A2UI:        a2ui,
		}
	}
	for _, tc := range []struct {
		name    string
		a2ui    *A2UIConfig
		wantErr string
	}{
		{name: "catalog only", a2ui: &A2UIConfig{Forms: true, Catalog: "catalog.json"}},
		{
			// 設定のキーは読み込み時に小文字になりうるので、大文字小文字は区別しない。
			name: "catalog with components",
			a2ui: &A2UIConfig{
				Forms:   true,
				Catalog: "catalog.json",
				Components: map[string]A2UIComponentConfig{
					"textfield": {Component: "Input", Properties: map[string]string{"label": "title"}},
					"Button":    {Component: "Action"},
				},
			},
		},
		{
			name: "components without catalog",
			a2ui: &A2UIConfig{
				Forms:      true,
				Components: map[string]A2UIComponentConfig{"TextField": {Component: "Input"}},
			},
			wantErr: "components requires catalog",
		},
		{
			name: "unknown role",
			a2ui: &A2UIConfig{
				Forms:      true,
				Catalog:    "catalog.json",
				Components: map[string]A2UIComponentConfig{"Slider": {Component: "Range"}},
			},
			wantErr: `unknown role "Slider"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := server(tc.a2ui)
			err := s.ValidateWithContext(t.Context())
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
