package mcpsrv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/stretchr/testify/require"
)

// resultTypeOf は res を JSON にしたときの resultType を返す。無ければ空文字。
// go-sdk の結果型は resultType を非公開に持ち、JSON の marshal でのみ外から読める。
func resultTypeOf(t *testing.T, res any) string {
	t.Helper()
	raw, err := json.Marshal(res)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	v, ok := fields[resultTypeKey]
	if !ok {
		return ""
	}
	var s string
	require.NoError(t, json.Unmarshal(v, &s))
	return s
}

// newLegacyProtocolBackend は 2026-07-28 より古い版でしか交渉しないバックエンドを
// 立てる。ゲートウェイのクライアントはこの版で交渉するため、バックエンドの
// tools/call・resources/read の結果に resultType は付かない。
func newLegacyProtocolBackend(t *testing.T) *httptest.Server {
	t.Helper()
	backend := mcp.NewServer(
		&mcp.Implementation{Name: "backend", Version: "0.0.1"},
		&mcp.ServerOptions{SupportedProtocolVersions: []string{legacyHostProtocolVersion}},
	)
	backend.AddTool(
		&mcp.Tool{Name: "show_counter", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: "1"}},
				StructuredContent: map[string]any{"count": 1},
				Meta:              mcp.Meta{"x-backend": "yes"},
			}, nil
		},
	)
	backend.AddResource(
		&mcp.Resource{URI: "ui://demo/counter.html", Name: "counter", MIMEType: mcpAppsMIMEType},
		func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
				URI: "ui://demo/counter.html", MIMEType: mcpAppsMIMEType, Text: "<html></html>",
			}}}, nil
		},
	)
	srv := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backend },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(srv.Close)
	return srv
}

// connectResultTypeGateway は backendURL を apps 付きの MCP バックエンドに持つ
// ゲートウェイを本番と同じ Stateless な Streamable HTTP で立て、protocolVersion
// （空なら最新）のホストとして接続する。
func connectResultTypeGateway(t *testing.T, backendURL, protocolVersion string) *mcp.ClientSession {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		config.Servers{"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       backendURL,
			Apps:      true,
		}},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)
	srv, err := s.Server("backend")
	require.NoError(t, err)
	gateway := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	t.Cleanup(gateway.Close)

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "host", Version: "0.0.1"}, nil).Connect(
		context.Background(),
		&mcp.StreamableClientTransport{Endpoint: gateway.URL},
		&mcp.ClientSessionOptions{ProtocolVersion: protocolVersion},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// 古い版で交渉するバックエンドを 2026-07-28 のクライアントから呼ぶと、tools/call と
// resources/read の応答に resultType: "complete" が付く（バックエンドの内容は保たれる）。
func TestRelayedResult_NewProtocolHost_LegacyBackend_AddsResultType(t *testing.T) {
	backend := newLegacyProtocolBackend(t)
	cs := connectResultTypeGateway(t, backend.URL, "")
	require.Equal(t, resultTypeProtocolVersion, cs.InitializeResult().ProtocolVersion)
	ctx := context.Background()

	call, err := cs.CallTool(
		ctx,
		&mcp.CallToolParams{Name: "show_counter", Arguments: map[string]any{}},
	)
	require.NoError(t, err)
	require.False(t, call.IsError)
	require.Equal(t, resultTypeComplete, resultTypeOf(t, call))
	require.Len(t, call.Content, 1)
	require.Equal(t, "1", call.Content[0].(*mcp.TextContent).Text)
	require.Equal(t, map[string]any{"count": float64(1)}, call.StructuredContent)
	require.Equal(t, "yes", call.Meta["x-backend"])

	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://demo/counter.html"})
	require.NoError(t, err)
	require.Equal(t, resultTypeComplete, resultTypeOf(t, read))
	require.Len(t, read.Contents, 1)
	require.Equal(t, "<html></html>", read.Contents[0].Text)
}

// 古い版のクライアントから呼ぶ場合は resultType を付けない。
func TestRelayedResult_LegacyHost_LegacyBackend_NoResultType(t *testing.T) {
	backend := newLegacyProtocolBackend(t)
	cs := connectResultTypeGateway(t, backend.URL, legacyHostProtocolVersion)
	require.Equal(t, legacyHostProtocolVersion, cs.InitializeResult().ProtocolVersion)
	ctx := context.Background()

	call, err := cs.CallTool(
		ctx,
		&mcp.CallToolParams{Name: "show_counter", Arguments: map[string]any{}},
	)
	require.NoError(t, err)
	require.Empty(t, resultTypeOf(t, call))

	read, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "ui://demo/counter.html"})
	require.NoError(t, err)
	require.Empty(t, resultTypeOf(t, read))
}

func TestWithCompleteResultType(t *testing.T) {
	t.Run("adds complete when missing", func(t *testing.T) {
		res := &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
			IsError: true,
		}
		got, err := withCompleteResultType(res)
		require.NoError(t, err)
		require.Equal(t, resultTypeComplete, resultTypeOf(t, got))
		require.True(t, got.IsError)
		require.Equal(t, "pong", got.Content[0].(*mcp.TextContent).Text)
		// 元の結果は書き換えない。
		require.Empty(t, resultTypeOf(t, res))
	})

	t.Run("keeps input_required", func(t *testing.T) {
		var res mcp.ReadResourceResult
		require.NoError(t, json.Unmarshal([]byte(`{
			"contents": [],
			"resultType": "input_required",
			"requestState": "s1",
			"inputRequests": {"q": {"method": "elicitation/create", "params": {"message": "name?"}}}
		}`), &res))
		require.True(t, res.NeedsInput())
		got, err := withCompleteResultType(&res)
		require.NoError(t, err)
		require.Same(t, &res, got)
		require.Equal(t, "input_required", resultTypeOf(t, got))
	})

	t.Run("keeps complete from backend", func(t *testing.T) {
		var res mcp.CallToolResult
		require.NoError(
			t,
			json.Unmarshal([]byte(`{"content": [], "resultType": "complete"}`), &res),
		)
		got, err := withCompleteResultType(&res)
		require.NoError(t, err)
		require.Same(t, &res, got)
		require.Equal(t, resultTypeComplete, resultTypeOf(t, got))
	})
}

func TestCallerRequiresResultType(t *testing.T) {
	require.False(t, callerRequiresResultType(nil))
	// セッションを持たないリクエスト（プロセス内で組み立てた場合）は補わない。
	require.False(t, callerRequiresResultType(&mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{Name: "x"},
	}))
}
