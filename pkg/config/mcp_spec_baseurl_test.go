package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServer_ValidateWithContext_LocalSpecBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		{"absolute server", "servers:\n  - url: https://api.example.com/v1\n", false},
		{"relative server only", "servers:\n  - url: /api/v1\n", true},
		{"no servers", "openapi: 3.0.0\n", true},
		{"swagger host", "swagger: '2.0'\nhost: api.example.com\n", false},
		{"swagger host unquoted version", "swagger: 2.0\nhost: api.example.com\n", false},
		{"openapi 3 host is not a base URL", "openapi: 3.0.0\nhost: api.example.com\n", true},
		{"json spec", `{"servers":[{"url":"https://api.example.com"}]}`, false},
		{"unparsable is left to startup", "{{{ not yaml", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Server{Description: "d", Spec: writeSpecFile(t, tt.spec)}
			err := s.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.ErrorContains(t, err, "set mcpServers.<name>.baseURL")
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("explicit baseURL wins over relative servers", func(t *testing.T) {
		s := Server{
			Description: "d",
			Spec:        writeSpecFile(t, "servers:\n  - url: /api/v1\n"),
			BaseURL:     "https://api.example.com",
			BaseURLSet:  true,
		}
		require.NoError(t, s.ValidateWithContext(t.Context()))
	})
	t.Run("missing file and remote specs are skipped", func(t *testing.T) {
		for _, spec := range []string{"/nonexistent/openapi.yaml", "configmap://ns/name/key"} {
			s := Server{Description: "d", Spec: spec}
			require.NoError(t, s.ValidateWithContext(t.Context()), spec)
		}
	})
}

func writeSpecFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
