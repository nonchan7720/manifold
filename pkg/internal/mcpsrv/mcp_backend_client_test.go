package mcpsrv

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
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

// --- MCPBackendClient.registerTools ---

func TestMCPBackendClient_RegisterTools_AddsToCatalog(t *testing.T) {
	ctx := t.Context()

	backendSrv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "test"}, &mcp.ServerOptions{})
	backendSrv.AddTool(&mcp.Tool{Name: "real_tool", Description: "a real tool", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})

	session := connectInMemory(t, ctx, backendSrv)

	gatewaySrv := mcp.NewServer(&mcp.Implementation{Name: "gateway", Version: "test"}, &mcp.ServerOptions{})
	catalog := toolsearch.NewCatalog()
	c := &MCPBackendClient{name: "test-backend", cfg: &config.Server{}, srv: gatewaySrv, catalog: catalog}

	err := c.registerTools(ctx, session)
	require.NoError(t, err)
	require.Equal(t, 1, catalog.Total())

	docs, err := catalog.Search("test-backend", "real_tool", toolsearch.MethodRegexp, 10)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	require.Equal(t, "real_tool", docs[0].Name)
}

func TestMCPBackendClient_RegisterTools_SkipsUpstreamToolSearchName(t *testing.T) {
	ctx := t.Context()

	backendSrv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "test"}, &mcp.ServerOptions{})
	backendSrv.AddTool(&mcp.Tool{Name: "tool_search", Description: "upstream tool_search", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	backendSrv.AddTool(&mcp.Tool{Name: "real_tool", Description: "a real tool", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})

	session := connectInMemory(t, ctx, backendSrv)

	gatewaySrv := mcp.NewServer(&mcp.Implementation{Name: "gateway", Version: "test"}, &mcp.ServerOptions{})
	catalog := toolsearch.NewCatalog()
	c := &MCPBackendClient{name: "test-backend", cfg: &config.Server{}, srv: gatewaySrv, catalog: catalog}

	err := c.registerTools(ctx, session)
	require.NoError(t, err)

	// upstream の "tool_search" は合成ツールの上書きを防ぐため catalog にも srv にも登録されない
	require.Equal(t, 1, catalog.Total())
	docs, err := catalog.Search("test-backend", "tool_search", toolsearch.MethodRegexp, 10)
	require.NoError(t, err)
	require.Empty(t, docs)

	// gatewaySrv 側にも登録されていないことを確認
	gwSession := connectInMemory(t, ctx, gatewaySrv)
	result, err := gwSession.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, result.Tools, 1)
	require.Equal(t, "real_tool", result.Tools[0].Name)
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
