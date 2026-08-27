package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// --- buildTransport ---

func TestBuildTransport_StdioEmptyCommand(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportStdio,
			Command:   "", // 空のコマンド
		},
	}

	_, err := c.buildTransport(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "command is required")
}

func TestBuildTransport_UnknownTransport(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: "unknown",
		},
	}

	_, err := c.buildTransport(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown transport")
}

func TestBuildTransport_HTTP_NoAuthValue(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

func TestBuildTransport_HTTP_WithAuthValue(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
			AuthValue: &config.AuthValue{
				Header: "Authorization",
				Prefix: "Bearer",
				Value:  "static-token",
			},
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

func TestBuildTransport_Stdio_WithCommand(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportStdio,
			Command:   "/bin/echo",
			Args:      []string{"hello"},
			Env:       map[string]string{"TEST_VAR": "value"},
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

// --- MCPBackendClient.Close ---

func TestMCPBackendClient_Close_NotConnected(t *testing.T) {
	c := &MCPBackendClient{
		name:      "test",
		cfg:       &config.Server{},
		connected: false,
		session:   nil,
	}
	// 接続していない場合はパニックしない
	require.NotPanics(t, func() {
		c.Close()
	})
	require.False(t, c.connected)
}

// --- MCPBackendClient session recovery ---

func TestMCPBackendClient_ReconnectsAfterSessionTerminated(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	backendSrv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	backendSrv.AddTool(
		&mcp.Tool{
			Name:        "ping",
			Description: "ping the backend",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	// ステートフルモード（セッション ID あり）でセッション失効を再現する
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendSrv },
		nil,
	))
	t.Cleanup(httpSrv.Close)

	c := &MCPBackendClient{
		name: "backend",
		cfg:  &config.Server{Transport: config.MCPTransportHTTP, URL: httpSrv.URL},
	}
	t.Cleanup(c.Close)

	_, err := c.ListTools(context.Background(), nil)
	require.NoError(t, err)

	// サーバー側でセッションを終了させる（バックエンド再起動相当）
	for ss := range backendSrv.Sessions() {
		require.NoError(t, ss.Close())
	}

	// 失効を検知するまでの呼び出しは失敗するが、失効セッションは破棄され、
	// その後の呼び出しは再接続して成功する。
	require.Eventually(t, func() bool {
		res, err := c.ListTools(context.Background(), nil)
		return err == nil && len(res.Tools) == 1
	}, 10*time.Second, 50*time.Millisecond,
		"ListTools should recover by reconnecting after session termination")
}

// --- MCPBackendClient.ListToolInfos ---

func newBackendCatalogServer(t *testing.T) (*httptest.Server, *mcp.Server) {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "ping",
			Description: "ping the backend",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	httpSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(httpSrv.Close)
	return httpSrv, srv
}

func TestMCPBackendClient_ListToolInfos_ConnectsLazily(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, _ := newBackendCatalogServer(t)

	c := &MCPBackendClient{
		name: "backend",
		cfg:  &config.Server{Transport: config.MCPTransportHTTP, URL: httpSrv.URL},
	}
	t.Cleanup(c.Close)

	infos, err := c.ListToolInfos(context.Background())
	require.NoError(t, err)
	require.Equal(t, []ToolInfo{{Name: "ping", Description: "ping the backend"}}, infos)
}

func TestMCPBackendClient_ListToolInfos_ReflectsBackendToolChanges(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, backendSrv := newBackendCatalogServer(t)

	c := &MCPBackendClient{
		name: "backend",
		cfg:  &config.Server{Transport: config.MCPTransportHTTP, URL: httpSrv.URL},
	}
	t.Cleanup(c.Close)

	infos, err := c.ListToolInfos(context.Background())
	require.NoError(t, err)
	require.Equal(t, []ToolInfo{{Name: "ping", Description: "ping the backend"}}, infos)

	// 接続後にバックエンド側でツールが増えても、次の問い合わせに反映される
	backendSrv.AddTool(
		&mcp.Tool{
			Name:        "echo",
			Description: "echo the input",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo"}}}, nil
		},
	)

	infos, err = c.ListToolInfos(context.Background())
	require.NoError(t, err)
	require.Contains(t, infos, ToolInfo{Name: "ping", Description: "ping the backend"})
	require.Contains(t, infos, ToolInfo{Name: "echo", Description: "echo the input"})
}
