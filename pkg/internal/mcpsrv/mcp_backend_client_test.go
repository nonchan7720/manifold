package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// --- IsPersistent ---

func TestMCPBackendClient_IsPersistent_Stdio(t *testing.T) {
	c := &MCPBackendClient{cfg: &config.Server{Transport: config.MCPTransportStdio}}
	require.True(t, c.IsPersistent())
}

func TestMCPBackendClient_IsPersistent_HTTP(t *testing.T) {
	c := &MCPBackendClient{cfg: &config.Server{Transport: config.MCPTransportHTTP}}
	require.False(t, c.IsPersistent())
}

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

// --- MCPBackendClient session recovery (stdio 共有セッションのみ意味を持つ) ---
//
// isDeadSessionError / invalidateSession による再接続処理は stdio の共有セッ
// ションにのみ意味がある。http は呼び出しごとに新しいセッションを張って即座
// にクローズする（完全ステートレス）ため、バックエンド側でのセッション終了は
// 後続の呼び出しに一切影響しない。以下はその新しい挙動の回帰テストであり、
// 「セッション終了後に再接続して復旧する」という旧テストの意図は http では
// 成立しなくなったため置き換えた（stdio 側の共有セッション破棄自体は
// invalidateSession の実装として引き続き存在する）。

func TestMCPBackendClient_HTTP_SessionTerminationDoesNotAffectNextCall(t *testing.T) {
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
	// ステートフルモード（セッション ID あり）
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

	res, err := c.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)

	// バックエンド側にセッションが残っていても（あるいは無くても）気にせず
	// 終了させる。http クライアントは呼び出しごとに新しいセッションを張るため、
	// 以前の呼び出しのセッションがどうなっていようと後続の呼び出しには
	// 影響しない（旧実装のように「失効を検知して再接続」という手順は不要）。
	for ss := range backendSrv.Sessions() {
		require.NoError(t, ss.Close())
	}
	res, err = c.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Len(t, res.Tools, 1)
}

// TestMCPBackendClient_HTTP_EachCallUsesIndependentSession は http バックエンドの
// 呼び出しが操作ごとに独立したセッション（別 initialize・別 Mcp-Session-Id）を
// 張ること、かつ呼び出し元 ctx の認証トークンがそのまま initialize を含む全
// リクエストに載ることを検証する。これがマルチテナント環境でのアイデンティ
// ティ混線バグの修正点そのもの。
func TestMCPBackendClient_HTTP_EachCallUsesIndependentSession(t *testing.T) {
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
	// ステートフルモード。Mcp-Session-Id は initialize のレスポンスにのみ付与される。
	inner := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return backendSrv },
		nil,
	)

	var mu sync.Mutex
	var sessionIDs []string
	var authHeaders []string
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()

		inner.ServeHTTP(w, r)

		if id := w.Header().Get("Mcp-Session-Id"); id != "" {
			mu.Lock()
			sessionIDs = append(sessionIDs, id)
			mu.Unlock()
		}
	}))
	t.Cleanup(httpSrv.Close)

	c := &MCPBackendClient{
		name: "backend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       httpSrv.URL,
			// oauth2 設定時は呼び出し元 ctx のトークンがそのまま転送される
			// （pkg/internal/client/oauth.go）。
			OAuth2: &config.OAuth2{},
		},
	}
	t.Cleanup(c.Close)

	ctxUserA := contexts.ToRequestAuthHeader(context.Background(), "Bearer token-user-a")
	_, err := c.ListTools(ctxUserA, nil)
	require.NoError(t, err)

	ctxUserB := contexts.ToRequestAuthHeader(context.Background(), "Bearer token-user-b")
	_, err = c.ListTools(ctxUserB, nil)
	require.NoError(t, err)

	// 2回の呼び出しはそれぞれ別の initialize ハンドシェイクを行い、
	// 別の Mcp-Session-Id を割り当てられている（セッションが共有されていない）。
	require.Len(t, sessionIDs, 2)
	require.NotEmpty(t, sessionIDs[0])
	require.NotEmpty(t, sessionIDs[1])
	require.NotEqual(t, sessionIDs[0], sessionIDs[1])

	// initialize を含む全リクエストに、その呼び出しを行った本人のトークンが載る。
	require.Contains(t, authHeaders, "Bearer token-user-a")
	require.Contains(t, authHeaders, "Bearer token-user-b")
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
