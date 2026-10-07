package mcpsrv

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// newPetAPIServer serves GET /pet/{id} for the petstore fixture, counting
// requests and recording the Authorization header of the last one.
func newPetAPIServer(t *testing.T, lastAuth *atomic.Value, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		lastAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"doggie","path":"` + r.URL.Path + `"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type petstoreTestServer struct {
	s        *MCPServer
	lastAuth *atomic.Value
	apiCalls *atomic.Int32
}

func newPetstoreTestMCPServer(
	t *testing.T, audit *AuditLogger, configure func(*config.Server),
) petstoreTestServer {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	var (
		lastAuth atomic.Value
		apiCalls atomic.Int32
	)
	petAPI := newPetAPIServer(t, &lastAuth, &apiCalls)

	server := &config.Server{
		Name:    "petstore",
		Spec:    "fixtures/petstore_oas.json",
		BaseURL: petAPI.URL,
		Tools: &config.ToolsConfig{
			Include:   []string{"getpetbyid", "findpetsbystatus"},
			Overrides: map[string]config.ToolOverride{"getpetbyid": {Name: "get_pet"}},
		},
	}
	if configure != nil {
		configure(server)
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		config.Servers{"petstore": server},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
		WithAuditLogger(audit),
	)
	require.NoError(t, s.Init(t.Context()))
	t.Cleanup(s.Close)
	return petstoreTestServer{s: s, lastAuth: &lastAuth, apiCalls: &apiCalls}
}

func (p petstoreTestServer) connect(t *testing.T, token string) *mcp.ClientSession {
	t.Helper()
	srv, err := p.s.Server("petstore")
	require.NoError(t, err)
	return connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), token), srv)
}

func TestMCPServer_Init_AppliesServerToolMiddlewares(t *testing.T) {
	var buf bytes.Buffer
	audit := newAuditLoggerTo(&buf, config.AuthzHeaders{}, false)
	p := newPetstoreTestMCPServer(t, audit, nil)
	cs := p.connect(t, "caller-token")

	require.ElementsMatch(t, []string{"get_pet", "findpetsbystatus"}, sessionToolNames(t, cs))

	// リネーム後の名前で呼べ、呼び出し元のトークンが転送される
	got := callText(t, cs, "get_pet", map[string]any{"petId": 1})
	require.Contains(t, got, `"path":"/pet/1"`)
	require.Equal(t, "Bearer caller-token", p.lastAuth.Load())

	// 同じ引数の 2 回目も毎回バックエンドへ届く
	callText(t, cs, "get_pet", map[string]any{"petId": 1})
	require.Equal(t, int32(2), p.apiCalls.Load())

	for _, name := range []string{"getpetbyid", "addpet"} {
		_, err := cs.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: name, Arguments: map[string]any{}},
		)
		require.ErrorContains(t, err, "unknown tool", name)
	}

	// 監査ログは公開名で残る
	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 4)
	require.Equal(t, "petstore", lines[0]["server"])
	require.Equal(t, "get_pet", lines[0]["tool"])
	require.Equal(t, AuditOutcomeSuccess, lines[1]["outcome"])
	require.Equal(t, AuditOutcomeError, lines[2]["outcome"])
}

func TestMCPServer_ToolCatalog_AppliesToolFilter(t *testing.T) {
	p := newPetstoreTestMCPServer(t, nil, nil)
	infos, err := p.s.ToolCatalog(t.Context(), "petstore")
	require.NoError(t, err)
	var names []string
	for _, info := range infos {
		names = append(names, info.Name)
	}
	require.ElementsMatch(t, []string{"get_pet", "findpetsbystatus"}, names)
}
