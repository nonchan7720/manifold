package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// newPetstoreStub serves the petstore OpenAPI fixture at /openapi.json (with
// servers pointing back at itself) and answers GET /api/v3/pet/{id}.
func newPetstoreStub(t *testing.T) *httptest.Server {
	t.Helper()
	spec, err := os.ReadFile("../internal/mcpsrv/fixtures/petstore_oas.json")
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(spec)
	})
	mux.HandleFunc("/api/v3/pet/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":` + r.PathValue("id") + `,"name":"doggie"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestParseQuickStartSpec(t *testing.T) {
	tests := []struct {
		arg, name, spec string
	}{
		{arg: "https://example.com/openapi.json", spec: "https://example.com/openapi.json"},
		{
			arg:  "pets=https://example.com/openapi.json",
			name: "pets",
			spec: "https://example.com/openapi.json",
		},
		{arg: "https://example.com/openapi.json?a=b", spec: "https://example.com/openapi.json?a=b"},
		{arg: "./spec.yaml", spec: "./spec.yaml"},
	}
	for _, tt := range tests {
		name, spec := parseQuickStartSpec(tt.arg)
		require.Equal(t, tt.name, name, tt.arg)
		require.Equal(t, tt.spec, spec, tt.arg)
	}
}

func TestQuickStartFlags_Config(t *testing.T) {
	f := quickStartFlags{
		specs:      []string{"https://example.com/openapi.json"},
		headers:    []string{"X-Api-Key: secret"},
		include:    []string{"get*"},
		toolSearch: true,
		port:       9999,
	}
	cfg, err := f.config(t.Context())
	require.NoError(t, err)
	srv := cfg.MCPServer["api"]
	require.NotNil(t, srv)
	require.Equal(t, "https://example.com/openapi.json", srv.Spec)
	require.Equal(t, map[string]string{"X-Api-Key": "secret"}, srv.ExtraHeaders)
	require.Equal(t, []string{"get*"}, srv.Tools.Include)
	require.True(t, srv.ToolSearch.IsEnabled())
	require.Equal(t, 9999, cfg.Gateway.Port)
	require.True(t, cfg.UsesEphemeralStore())
	require.NotEmpty(t, cfg.Gateway.EncryptKey)
	require.False(t, cfg.Gateway.Aggregate.Enabled, "a single server needs no aggregate")
}

func TestQuickStartFlags_Config_MultipleSpecsAggregate(t *testing.T) {
	f := quickStartFlags{specs: []string{
		"pets=https://example.com/openapi.json",
		"https://example.com/other.json",
	}}
	cfg, err := f.config(t.Context())
	require.NoError(t, err)
	require.Contains(t, cfg.MCPServer, "pets")
	require.Contains(t, cfg.MCPServer, "api2")
	require.True(t, cfg.Gateway.Aggregate.Enabled)
}

func TestQuickStartFlags_Config_Errors(t *testing.T) {
	tests := []struct {
		name    string
		flags   quickStartFlags
		wantErr string
	}{
		{
			name: "base-url with several specs",
			flags: quickStartFlags{
				specs: []string{
					"a=https://a.example.com/o.json",
					"b=https://b.example.com/o.json",
				},
				baseURL: "https://a.example.com",
			},
			wantErr: "single --openapi",
		},
		{
			name: "bad header",
			flags: quickStartFlags{
				specs:   []string{"https://a.example.com/o.json"},
				headers: []string{"nocolon"},
			},
			wantErr: `want "Name: value"`,
		},
		{
			name: "duplicate name",
			flags: quickStartFlags{specs: []string{
				"x=https://a.example.com/o.json", "x=https://b.example.com/o.json",
			}},
			wantErr: "more than once",
		},
		{
			name: "bad pattern",
			flags: quickStartFlags{
				specs:   []string{"https://a.example.com/o.json"},
				include: []string{"["},
			},
			wantErr: "invalid tool pattern",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.flags.config(t.Context())
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestResolveStdioTarget(t *testing.T) {
	openapi := func() *config.Server {
		return &config.Server{Spec: "https://example.com/openapi.json"}
	}
	cfg := &config.Config{MCPServer: config.Servers{
		"pets":  openapi(),
		"users": openapi(),
		"login": {Spec: "https://example.com/openapi.json", OAuth2: &config.OAuth2{}},
		"exch":  {Spec: "https://example.com/openapi.json", TokenExchange: &config.TokenExchange{}},
	}}

	target, err := resolveStdioTarget(t.Context(), cfg, "pets")
	require.NoError(t, err)
	require.Equal(t, stdioTarget{single: "pets"}, target)

	_, err = resolveStdioTarget(t.Context(), cfg, "login")
	require.ErrorContains(t, err, "oauth2")
	_, err = resolveStdioTarget(t.Context(), cfg, "missing")
	require.ErrorContains(t, err, "not defined")

	// --server 無し・複数サーバーは stdio で動くものだけを集約する
	target, err = resolveStdioTarget(t.Context(), cfg, "")
	require.NoError(t, err)
	require.Equal(t, stdioTarget{members: []string{"pets", "users"}}, target)

	// aggregate.servers で明示した stdio 非対応のサーバーはエラー
	cfg.Gateway.Aggregate.Servers = []string{"pets", "exch"}
	_, err = resolveStdioTarget(t.Context(), cfg, "")
	require.ErrorContains(t, err, "tokenExchange")

	single := &config.Config{MCPServer: config.Servers{"only": openapi()}}
	target, err = resolveStdioTarget(t.Context(), single, "")
	require.NoError(t, err)
	require.Equal(t, stdioTarget{single: "only"}, target)

	clash := &config.Config{MCPServer: config.Servers{"a__b": openapi(), "c": openapi()}}
	_, err = resolveStdioTarget(t.Context(), clash, "")
	require.ErrorContains(t, err, "separator")
}

// runStdioForTest runs runStdio on an in-memory transport and returns a
// connected client.
func runStdioForTest(t *testing.T, cfg *config.Config, server string) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	errCh := make(chan error, 1)
	go func() { errCh <- runStdio(t.Context(), cfg, server, serverTransport) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	cs, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cs.Close()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("runStdio did not return after the client disconnected")
		}
	})
	return cs
}

func TestRunStdio_QuickStartOpenAPI(t *testing.T) {
	t.Setenv("TEST", "true") // httptest (127.0.0.1) への接続を許可する
	stub := newPetstoreStub(t)
	cfg, err := (&quickStartFlags{
		specs:   []string{stub.URL + "/openapi.json"},
		baseURL: stub.URL + "/api/v3",
		include: []string{"getpetbyid", "findpets*"},
	}).config(t.Context())
	require.NoError(t, err)
	withGlobalConfig(t, cfg)

	cs := runStdioForTest(t, cfg, "")
	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	require.ElementsMatch(t, []string{"getpetbyid", "findpetsbystatus", "findpetsbytags"}, names)

	call, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "getpetbyid", Arguments: map[string]any{"petId": 7},
	})
	require.NoError(t, err)
	require.False(t, call.IsError)
	require.Contains(t, call.Content[0].(*mcp.TextContent).Text, `"id":7`)
}

func TestRunStdio_AggregatesSeveralServers(t *testing.T) {
	t.Setenv("TEST", "true")
	stub := newPetstoreStub(t)
	cfg, err := (&quickStartFlags{
		specs: []string{
			"pets=" + stub.URL + "/openapi.json",
			"store=" + stub.URL + "/openapi.json",
		},
		include: []string{"getpetbyid"},
	}).config(t.Context())
	require.NoError(t, err)
	withGlobalConfig(t, cfg)

	cs := runStdioForTest(t, cfg, "")
	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	require.Equal(t, []string{"pets__getpetbyid", "store__getpetbyid"}, names)

	// --base-url 無しでも spec の servers（相対 URL は spec の URL 基準）から導出される
	call, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "store__getpetbyid", Arguments: map[string]any{"petId": 3},
	})
	require.NoError(t, err)
	require.False(t, call.IsError)
	require.Contains(t, call.Content[0].(*mcp.TextContent).Text, `"id":3`)
}

func TestRunStdio_RejectsAuthzAndStdoutAudit(t *testing.T) {
	cfg := &config.Config{
		MCPServer: config.Servers{"a": {Spec: "https://example.com/openapi.json"}},
		Authz:     config.AuthzConfig{Enabled: true},
	}
	err := runStdio(t.Context(), cfg, "", nil)
	require.ErrorContains(t, err, "authz is not supported")

	cfg.Authz.Enabled = false
	cfg.Audit = config.AuditConfig{Enabled: true, Output: config.AuditOutputStdout}
	err = runStdio(t.Context(), cfg, "", nil)
	require.ErrorContains(t, err, "can't be stdout")
}
