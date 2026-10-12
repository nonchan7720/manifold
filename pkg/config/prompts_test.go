package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServer_ValidateWithContext_Prompts(t *testing.T) {
	openAPI := func(p *PromptsConfig) Server {
		return Server{
			Description: "test server",
			Spec:        "spec.json",
			BaseURL:     "https://api.example.com",
			Prompts:     p,
		}
	}
	mcpBackend := baseValidServer()
	mcpBackend.Prompts = &PromptsConfig{File: "prompts.yaml"}

	for _, tc := range []struct {
		name    string
		server  Server
		wantErr string
	}{
		{name: "unset", server: openAPI(nil)},
		{name: "file", server: openAPI(&PromptsConfig{File: "/etc/manifold/prompts.yaml"})},
		{name: "url", server: openAPI(&PromptsConfig{URL: "https://api.example.com/prompts"})},
		{
			name:    "neither",
			server:  openAPI(&PromptsConfig{}),
			wantErr: "one of url or file is required",
		},
		{
			name:    "both",
			server:  openAPI(&PromptsConfig{URL: "https://api.example.com/prompts", File: "p.yaml"}),
			wantErr: "url and file are mutually exclusive",
		},
		{
			name:    "relative url",
			server:  openAPI(&PromptsConfig{URL: "/prompts"}),
			wantErr: "must be an absolute http(s) URL",
		},
		{
			name:    "non-http url",
			server:  openAPI(&PromptsConfig{URL: "file:///etc/prompts.yaml"}),
			wantErr: "must be an absolute http(s) URL",
		},
		{
			name:    "mcp backend",
			server:  mcpBackend,
			wantErr: "is only supported in OpenAPI mode",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.server.ValidateWithContext(t.Context())
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
