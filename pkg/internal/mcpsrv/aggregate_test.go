package mcpsrv

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// newPetAPIServer serves GET /pet/{id} for the petstore fixture, recording
// the Authorization header of the last request.
func newPetAPIServer(t *testing.T, lastAuth *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"doggie","path":"` + r.URL.Path + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newAggregateTestMCPServer(t *testing.T, audit *AuditLogger) (*MCPServer, *atomic.Value) {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	backend, _ := newToolCatalogBackendServer(t, 0)
	var lastAuth atomic.Value
	petAPI := newPetAPIServer(t, &lastAuth)

	servers := config.Servers{
		"petstore": {
			Name:    "petstore",
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: petAPI.URL,
			Tools: &config.ToolsConfig{
				Include:   []string{"getpetbyid", "findpetsbystatus"},
				Overrides: map[string]config.ToolOverride{"getpetbyid": {Name: "get_pet"}},
			},
		},
		"backend": {
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       backend.URL,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		servers,
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
		WithAuditLogger(audit),
		WithToolCache(NewToolCache(0)),
	)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	return s, &lastAuth
}

func TestMCPServer_NewAggregateServer(t *testing.T) {
	var buf bytes.Buffer
	audit := newAuditLoggerTo(&buf, config.AuthzHeaders{}, false)
	s, lastAuth := newAggregateTestMCPServer(t, audit)

	agg, err := s.NewAggregateServer(t.Context(), []string{"backend", "petstore"}, "__", nil)
	require.NoError(t, err)
	cs := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "caller-token"), agg)

	require.Equal(t,
		[]string{"backend__ping", "petstore__findpetsbystatus", "petstore__get_pet"},
		slices.Sorted(slices.Values(sessionToolNames(t, cs))),
	)

	require.Equal(t, "pong", callText(t, cs, "backend__ping", map[string]any{}))
	got := callText(t, cs, "petstore__get_pet", map[string]any{"petId": 1})
	require.Contains(t, got, `"path":"/pet/1"`)
	// 呼び出し元のトークンはメンバーの /mcp/{server_name} と同じく転送される
	require.Equal(t, "Bearer caller-token", lastAuth.Load())

	for _, name := range []string{"petstore__getpetbyid", "petstore__addpet", "nope__ping", "ping", "backend__"} {
		_, err := cs.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: name, Arguments: map[string]any{}},
		)
		require.ErrorContains(t, err, "unknown tool", name)
	}

	// 監査ログはメンバーの実際のサーバー名・ツール名で残る
	var servers, tools []string
	for _, line := range decodeAuditLines(t, &buf) {
		servers = append(servers, line["server"].(string))
		tools = append(tools, line["tool"].(string))
	}
	require.Contains(t, servers, "backend")
	require.Contains(t, tools, "get_pet")
}

func TestMCPServer_NewAggregateServer_ToolSearch(t *testing.T) {
	s, _ := newAggregateTestMCPServer(t, nil)
	agg, err := s.NewAggregateServer(
		t.Context(), []string{"backend", "petstore"}, "__", &config.ToolSearchConfig{Enabled: true},
	)
	require.NoError(t, err)
	cs := connectTestClient(t, t.Context(), agg)

	require.Equal(t, []string{ToolSearchToolName, ToolCallToolName}, sessionToolNames(t, cs))

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: ToolSearchToolName, Arguments: map[string]any{"query": "ping"},
	})
	require.NoError(t, err)
	var out toolSearchOutput
	raw, _ := json.Marshal(res.StructuredContent)
	require.NoError(t, json.Unmarshal(raw, &out))
	require.Equal(t, 1, out.Total)
	require.Equal(t, "backend__ping", out.Tools[0].Name)

	require.Equal(
		t,
		"pong",
		callText(t, cs, ToolCallToolName, map[string]any{"name": "backend__ping"}),
	)
}

func TestMCPServer_NewAggregateServer_UnknownMember(t *testing.T) {
	s, _ := newAggregateTestMCPServer(t, nil)
	_, err := s.NewAggregateServer(t.Context(), []string{"petstore", "missing"}, "__", nil)
	require.ErrorContains(t, err, `"missing" is not available`)
}

func TestMCPServer_ToolCatalog_AppliesToolFilter(t *testing.T) {
	s, _ := newAggregateTestMCPServer(t, nil)
	infos, err := s.ToolCatalog(t.Context(), "petstore")
	require.NoError(t, err)
	var names []string
	for _, info := range infos {
		names = append(names, info.Name)
	}
	require.ElementsMatch(t, []string{"get_pet", "findpetsbystatus"}, names)
}

func TestMCPServer_PerServerToolSearch(t *testing.T) {
	t.Setenv("TEST", "true")
	servers := config.Servers{
		"petstore": {
			Name:       "petstore",
			Spec:       "fixtures/petstore_oas.json",
			BaseURL:    "https://petstore.example.com",
			ToolSearch: &config.ToolSearchConfig{Enabled: true},
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	srv, err := s.Server("petstore")
	require.NoError(t, err)
	cs := connectTestClient(t, t.Context(), srv)
	require.Equal(t, []string{ToolSearchToolName, ToolCallToolName}, sessionToolNames(t, cs))
}
