package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/stretchr/testify/require"
)

// 2026-07-28 以降のプロトコルのホストが付けるリクエストごとの _meta を
// バックエンドへ転送せず、Stateful な（go-sdk 既定の）http バックエンドにも
// tools/list・tools/call と resources/* が届くことを検証する。
func TestBackendPassthrough_NewProtocolHost_StatefulBackend(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	backend := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	backend.AddTool(
		&mcp.Tool{Name: "ping", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	backend.AddResource(
		&mcp.Resource{URI: "ui://test/app.html", Name: "app", MIMEType: mcpAppsMIMEType},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: "ui://test/app.html", MIMEType: mcpAppsMIMEType, Text: "<html></html>",
			}}}, nil
		},
	)
	backendSrv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backend },
		nil, // Stateful
	))
	t.Cleanup(backendSrv.Close)

	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		config.Servers{"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       backendSrv.URL,
		}},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)
	srv, err := s.Server("backend")
	require.NoError(t, err)
	// 本番と同じ Stateless な Streamable HTTP で配信する。
	gateway := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(gateway.Close)

	ctx := context.Background()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "host", Version: "0.0.1"}, nil).
		Connect(ctx, &mcp.StreamableClientTransport{Endpoint: gateway.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	require.Equal(t, "2026-07-28", cs.InitializeResult().ProtocolVersion)

	tools, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, res.IsError)

	resources, err := cs.ListResources(ctx, nil)
	require.NoError(t, err)
	require.Len(t, resources.Resources, 1)

	_, err = cs.ListResourceTemplates(ctx, nil)
	require.NoError(t, err)

	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://test/app.html"})
	require.NoError(t, err)
	require.Len(t, read.Contents, 1)
}

func TestWithoutProtocolMeta(t *testing.T) {
	meta := func(p *mcp.ListToolsParams) *mcp.Meta { return &p.Meta }
	require.Nil(t, withoutProtocolMeta(nil, meta))

	params := &mcp.ListToolsParams{
		Cursor: "next",
		Meta: mcp.Meta{
			mcp.MetaKeyProtocolVersion:    "2026-07-28",
			mcp.MetaKeyClientCapabilities: map[string]any{},
			"progressToken":               "p1",
		},
	}
	got := withoutProtocolMeta(params, meta)
	require.Equal(t, "next", got.Cursor)
	require.Equal(t, mcp.Meta{"progressToken": "p1"}, got.Meta)
	// 元の params は書き換えない。
	require.Len(t, params.Meta, 3)

	onlyProtocol := &mcp.ListToolsParams{Meta: mcp.Meta{mcp.MetaKeyProtocolVersion: "2026-07-28"}}
	require.Nil(t, withoutProtocolMeta(onlyProtocol, meta).Meta)
}
