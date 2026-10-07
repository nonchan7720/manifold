package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/stretchr/testify/require"
)

const testAppResourceURI = "ui://weather/dashboard.html"

// appsBackend は MCP Apps のツールと ui:// リソースを持つバックエンド。
// 受け取ったリクエストの呼び出し元が UI 拡張を広告していたかを記録する。
type appsBackend struct {
	mu       sync.Mutex
	uiByCall map[string]bool
}

func (b *appsBackend) record(method string, caps *mcp.ClientCapabilities) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := caps.Extensions[mcpAppsExtension]
	b.uiByCall[method] = caps != nil && ok
}

func (b *appsBackend) sawUI(method string) (bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.uiByCall[method]
	return v, ok
}

func newAppsBackendServer(t *testing.T, withResources bool) (*httptest.Server, *appsBackend) {
	t.Helper()
	b := &appsBackend{uiByCall: map[string]bool{}}
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "get_weather",
			Description: "show the weather dashboard",
			InputSchema: map[string]any{"type": "object"},
			Meta: mcp.Meta{
				"ui": map[string]any{"resourceUri": testAppResourceURI},
			},
		},
		func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			b.record("tools/call", req.ClientCapabilities())
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "sunny"}}}, nil
		},
	)
	srv.AddTool(
		&mcp.Tool{
			Name:        "refresh_dashboard",
			Description: "called only from the dashboard UI",
			InputSchema: map[string]any{"type": "object"},
			Meta: mcp.Meta{
				"ui": map[string]any{
					"resourceUri": testAppResourceURI,
					"visibility":  []any{"app"},
				},
			},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
		},
	)
	if withResources {
		srv.AddResource(
			&mcp.Resource{URI: testAppResourceURI, Name: "dashboard", MIMEType: mcpAppsMIMEType},
			func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
				b.record("resources/read", req.ClientCapabilities())
				return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
					URI:      testAppResourceURI,
					MIMEType: mcpAppsMIMEType,
					Text:     "<html><body>weather</body></html>",
				}}}, nil
			},
		)
	}
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	return httpSrv, b
}

// 古いプロトコル（initialize で一度だけ capability を申告する）のホストを表す。
const legacyHostProtocolVersion = "2025-11-25"

// connectGateway は backendURL を MCP バックエンドに持つゲートウェイを本番と同じ
// Stateless な Streamable HTTP で立て、uiCapable なら UI 拡張を申告するホストとして
// protocolVersion（空なら最新）で接続する。
func connectGateway(
	t *testing.T, backendURL string, uiCapable, apps bool, protocolVersion string,
) *mcp.ClientSession {
	t.Helper()
	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       backendURL,
			Apps:      apps,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)
	srv, err := s.Server("backend")
	require.NoError(t, err)
	gateway := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(gateway.Close)

	var opts *mcp.ClientOptions
	if uiCapable {
		caps := &mcp.ClientCapabilities{}
		caps.AddExtension(mcpAppsExtension, defaultMCPAppsExtensionSettings())
		opts = &mcp.ClientOptions{Capabilities: caps}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "host", Version: "0.0.1"}, opts).Connect(
		context.Background(),
		&mcp.StreamableClientTransport{Endpoint: gateway.URL},
		&mcp.ClientSessionOptions{ProtocolVersion: protocolVersion},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// リクエストごとに UI 対応を申告するホスト（2026-07-28 以降）には、resources
// capability を広告し、ツールの _meta.ui をそのまま返し、ui:// リソースを
// resources/read で返す。
func TestMCPApps_HTTPBackend_ForwardsUIResources(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, backend := newAppsBackendServer(t, true)
	cs := connectGateway(t, httpSrv.URL, true, false, "")
	ctx := context.Background()

	require.NotNil(t, cs.InitializeResult().Capabilities.Resources)

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	// UI からしか呼ばないツールも含め、_meta.ui をそのまま返す。
	require.Len(t, tools.Tools, 2)
	for _, tool := range tools.Tools {
		ui, ok := tool.Meta["ui"].(map[string]any)
		require.True(t, ok, tool.Name)
		require.Equal(t, testAppResourceURI, ui["resourceUri"])
	}

	resources, err := cs.ListResources(ctx, nil)
	require.NoError(t, err)
	require.Len(t, resources.Resources, 1)
	require.Equal(t, testAppResourceURI, resources.Resources[0].URI)

	templates, err := cs.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, templates.ResourceTemplates)

	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: testAppResourceURI})
	require.NoError(t, err)
	require.Len(t, read.Contents, 1)
	require.Equal(t, mcpAppsMIMEType, read.Contents[0].MIMEType)
	require.Contains(t, read.Contents[0].Text, "weather")

	_, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_weather", Arguments: map[string]any{}})
	require.NoError(t, err)

	// 呼び出し元の UI 拡張がバックエンドまで届いている。
	for _, method := range []string{"resources/read", "tools/call"} {
		saw, ok := backend.sawUI(method)
		require.True(t, ok, method)
		require.True(t, saw, method)
	}
}

// UI 対応を申告しない呼び出し元には、http バックエンドへも UI 拡張を広告せず、
// tools/list から UI を取り除く。
func TestMCPApps_HTTPBackend_StripsUIForNonUICaller(t *testing.T) {
	t.Setenv("TEST", "true")
	httpSrv, backend := newAppsBackendServer(t, true)
	cs := connectGateway(t, httpSrv.URL, false, false, "")
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	require.Equal(t, "get_weather", tools.Tools[0].Name)
	require.NotContains(t, tools.Tools[0].Meta, "ui")

	_, err = cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: testAppResourceURI})
	require.NoError(t, err)
	_, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_weather", Arguments: map[string]any{}})
	require.NoError(t, err)

	for _, method := range []string{"resources/read", "tools/call"} {
		saw, ok := backend.sawUI(method)
		require.True(t, ok, method)
		require.False(t, saw, method)
	}
}

// 古いプロトコルのホストが initialize で申告した UI 対応は、Stateless 配信では
// 以降のリクエストで分からないため、UI 非対応として扱う。
func TestMCPApps_HTTPBackend_LegacyHostDeclarationIsNotVisible(t *testing.T) {
	t.Setenv("TEST", "true")
	httpSrv, _ := newAppsBackendServer(t, true)
	cs := connectGateway(t, httpSrv.URL, true, false, legacyHostProtocolVersion)

	tools, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	require.NotContains(t, tools.Tools[0].Meta, "ui")
}

// mcpServers.<name>.apps を有効にすると、申告の無い（古いプロトコルの）ホストも
// UI 対応とみなす。
func TestMCPApps_HTTPBackend_AppsConfigAssumesSupport(t *testing.T) {
	t.Setenv("TEST", "true")
	httpSrv, backend := newAppsBackendServer(t, true)
	cs := connectGateway(t, httpSrv.URL, false, true, legacyHostProtocolVersion)
	ctx := context.Background()

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 2)
	require.Contains(t, tools.Tools[0].Meta, "ui")

	_, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "get_weather", Arguments: map[string]any{}})
	require.NoError(t, err)
	saw, ok := backend.sawUI("tools/call")
	require.True(t, ok)
	require.True(t, saw)
}

// resources を持たないバックエンドでは、resources/list は空、resources/read は
// resource not found を返す（method not found にしない）。
func TestMCPApps_HTTPBackend_WithoutResources(t *testing.T) {
	t.Setenv("TEST", "true")
	httpSrv, _ := newAppsBackendServer(t, false)
	cs := connectGateway(t, httpSrv.URL, true, false, "")
	ctx := context.Background()

	resources, err := cs.ListResources(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, resources.Resources)

	templates, err := cs.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)
	require.Empty(t, templates.ResourceTemplates)

	_, err = cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: testAppResourceURI})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Resource not found")
}

func TestMCPBackendClient_ClientOptions(t *testing.T) {
	t.Run("http without caller extension", func(t *testing.T) {
		c := &MCPBackendClient{cfg: &config.Server{Transport: config.MCPTransportHTTP}}
		require.Nil(t, c.clientOptions(context.Background()))
	})
	t.Run("http forwards caller extension", func(t *testing.T) {
		c := &MCPBackendClient{cfg: &config.Server{Transport: config.MCPTransportHTTP}}
		settings := map[string]any{"mimeTypes": []any{mcpAppsMIMEType}}
		ctx := context.WithValue(context.Background(), callerUIExtensionKey{}, settings)
		opts := c.clientOptions(ctx)
		require.NotNil(t, opts)
		require.Equal(t, settings, opts.Capabilities.Extensions[mcpAppsExtension])
	})
	t.Run("stdio always advertises", func(t *testing.T) {
		c := &MCPBackendClient{cfg: &config.Server{Transport: config.MCPTransportStdio}}
		opts := c.clientOptions(context.Background())
		require.NotNil(t, opts)
		require.Equal(t, defaultMCPAppsExtensionSettings(),
			opts.Capabilities.Extensions[mcpAppsExtension])
	})
}

func TestStripMCPAppsTools(t *testing.T) {
	plain := &mcp.Tool{Name: "plain", Meta: mcp.Meta{"other": 1}}
	withUI := &mcp.Tool{Name: "with_ui", Meta: mcp.Meta{
		"ui":    map[string]any{"resourceUri": testAppResourceURI},
		"other": 1,
	}}
	legacy := &mcp.Tool{Name: "legacy", Meta: mcp.Meta{"ui/resourceUri": testAppResourceURI}}
	both := &mcp.Tool{Name: "both", Meta: mcp.Meta{
		"ui": map[string]any{
			"resourceUri": testAppResourceURI,
			"visibility":  []any{"model", "app"},
		},
	}}
	appOnly := &mcp.Tool{Name: "app_only", Meta: mcp.Meta{
		"ui": map[string]any{"resourceUri": testAppResourceURI, "visibility": []any{"app"}},
	}}

	out := stripMCPAppsTools([]*mcp.Tool{plain, withUI, legacy, both, appOnly})
	require.Len(t, out, 4)
	require.Same(t, plain, out[0])
	require.Equal(t, "with_ui", out[1].Name)
	require.Equal(t, mcp.Meta{"other": 1}, out[1].Meta)
	require.Equal(t, "legacy", out[2].Name)
	require.Nil(t, out[2].Meta)
	require.Equal(t, "both", out[3].Name)
	require.Nil(t, out[3].Meta)
	// 元のツールは書き換えない。
	require.Contains(t, withUI.Meta, "ui")
	require.Contains(t, legacy.Meta, "ui/resourceUri")
}
