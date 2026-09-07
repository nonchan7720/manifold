package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
)

// toolsFileTestSpec is a single-operation OpenAPI 3 spec used to build the
// generated tools files these tests start a gateway from.
const toolsFileTestSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Widget API", "version": "1.0.0"},
  "paths": {
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

// writeGeneratedToolsFile builds a generated tools file for toolsFileTestSpec
// (baseURL baked into the tool functions via BuildCatalog) and returns its
// path. mutate, if non-nil, is applied to the catalog before it's written —
// used to simulate a generated file that has gone stale relative to its spec.
func writeGeneratedToolsFile(
	t *testing.T, baseURL string, mutate func(g *oastomcptool.GeneratedCatalog),
) string {
	t.Helper()
	specPath := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(specPath, []byte(toolsFileTestSpec), 0o600))

	source, err := oastomcptool.LoadSpecSource(context.Background(), specPath)
	require.NoError(t, err)
	registry, err := BuildCatalog(context.Background(), &http.Client{}, source, baseURL, nil)
	require.NoError(t, err)

	g, err := oastomcptool.NewGeneratedCatalog(
		context.Background(), source, GeneratedTools(registry.Definitions()),
		"manifold test", time.Now(),
	)
	require.NoError(t, err)
	if mutate != nil {
		mutate(g)
	}

	genPath := filepath.Join(t.TempDir(), "generated.yaml")
	f, err := os.Create(genPath)
	require.NoError(t, err)
	require.NoError(t, oastomcptool.WriteGeneratedCatalog(f, g))
	require.NoError(t, f.Close())
	return genPath
}

func connectSession(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// TestMCPServer_Init_ToolsFile_UnreachableSpecStillStarts covers the core
// promise of tools.file: Init succeeds (and tools/list + tools/call work
// against the real backend) even though config.spec points nowhere
// reachable, because Init never fetches it when tools.file is set.
func TestMCPServer_Init_ToolsFile_UnreachableSpecStillStarts(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"42","name":"widget-42"}`))
	}))
	defer backend.Close()

	genPath := writeGeneratedToolsFile(t, backend.URL, nil)

	servers := config.Servers{
		"petstore": &config.Server{
			Name:    "petstore",
			Spec:    "http://127.0.0.1:1/openapi.json", // 到達不能
			BaseURL: backend.URL,
			Tools:   &config.ToolsConfig{File: genPath},
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))

	srv, err := s.Server("petstore")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"getwidget"}, listToolNames(t, srv))

	session := connectSession(t, srv)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "getwidget",
		Arguments: map[string]any{"id": "42"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.JSONEq(t, `{"id":"42","name":"widget-42"}`, text.Text)
}

// TestMCPServer_Init_ToolsFile_NoSpecStillStarts covers the config-level
// decision that spec is entirely optional once tools.file is set (see
// config.Server.IsOpenAPI): with no spec configured at all (not merely
// unreachable), Init still succeeds, tools/list shows the tools, tools/call
// still reaches the real backend, and StartSpecRefresh starts no refresh
// goroutine for it.
func TestMCPServer_Init_ToolsFile_NoSpecStillStarts(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"42","name":"widget-42"}`))
	}))
	defer backend.Close()

	genPath := writeGeneratedToolsFile(t, backend.URL, nil)

	servers := config.Servers{
		"petstore": &config.Server{
			Name:    "petstore",
			BaseURL: backend.URL,
			Tools:   &config.ToolsConfig{File: genPath},
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))

	srv, err := s.Server("petstore")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"getwidget"}, listToolNames(t, srv))

	session := connectSession(t, srv)
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "getwidget",
		Arguments: map[string]any{"id": "42"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.JSONEq(t, `{"id":"42","name":"widget-42"}`, text.Text)

	// No spec at all means nothing to refresh from: EffectiveSpecRefreshInterval
	// returns 0 for a server with Spec == "", so StartSpecRefresh must not
	// start a goroutine for it, mirroring
	// TestMCPServer_StartSpecRefresh_ToolsFile_NeverStartsGoroutine (which
	// uses a reachable-but-unused spec instead of no spec at all).
	require.Equal(
		t, time.Duration(0), servers["petstore"].EffectiveSpecRefreshInterval(20*time.Millisecond),
	)
	s.StartSpecRefresh(t.Context(), 20*time.Millisecond)
	defer s.Close()
	s.mu.Lock()
	cancelSet := s.refreshCancel != nil
	s.mu.Unlock()
	require.True(t, cancelSet, "StartSpecRefresh always sets refreshCancel, even with no targets")
}

// TestMCPServer_Init_ToolsFile_Stale asserts Init fails, naming both the
// server and the regeneration hint, when the generated file's "tools"
// section no longer matches what its own spec produces.
func TestMCPServer_Init_ToolsFile_Stale(t *testing.T) {
	t.Setenv("TEST", "true")

	genPath := writeGeneratedToolsFile(
		t, "https://api.example.com",
		func(g *oastomcptool.GeneratedCatalog) {
			for i := range g.Tools {
				g.Tools[i].Description = "stale description that no longer matches the spec"
			}
		},
	)

	servers := config.Servers{
		"petstore": &config.Server{
			Name:    "petstore",
			Spec:    "http://127.0.0.1:1/openapi.json",
			BaseURL: "https://api.example.com",
			Tools:   &config.ToolsConfig{File: genPath},
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(t.Context())
	require.Error(t, err)
	require.ErrorContains(t, err, `"petstore"`)
	require.ErrorContains(t, err, "manifold openapi generate")
}

// TestMCPServer_StartSpecRefresh_ToolsFile_NeverStartsGoroutine asserts a
// tools.file server never gets a refresh goroutine, even when
// gateway.specRefresh.interval is set — observed the same way
// spec_refresh_test.go observes "never refreshes": no fetch reaches the
// spec URL, here made watchable by pointing spec at an httptest server
// instead of a genuinely unreachable address.
func TestMCPServer_StartSpecRefresh_ToolsFile_NeverStartsGoroutine(t *testing.T) {
	t.Setenv("TEST", "true")

	spec := newSpecTestServer(t, specWithOperations("ping"))
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	genPath := writeGeneratedToolsFile(t, backend.URL, nil)

	servers := config.Servers{
		"petstore": &config.Server{
			Name:    "petstore",
			Spec:    spec.URL + "/openapi.json",
			BaseURL: backend.URL,
			Tools:   &config.ToolsConfig{File: genPath},
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))
	require.Equal(
		t, int64(0), spec.fetches.Load(), "Init must not fetch spec when tools.file is set",
	)

	// グローバル既定が正でも tools.file サーバーはリフレッシュ対象から外れる。
	s.StartSpecRefresh(t.Context(), 20*time.Millisecond)
	defer s.Close()

	time.Sleep(200 * time.Millisecond)
	require.Equal(
		t, int64(0), spec.fetches.Load(),
		"no refresh goroutine should start for a tools.file server",
	)
}
