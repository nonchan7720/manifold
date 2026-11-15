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
