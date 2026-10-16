package mcpsrv

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// testConnCounter はテスト用バックエンドが受けた接続の開始・終了数。
type testConnCounter struct {
	opened atomic.Int32
	closed atomic.Int32
}

// Load は新規接続数を返す。
func (c *testConnCounter) Load() int32 { return c.opened.Load() }

// newTestMCPHTTPBackend は slow ツールを持つ MCP バックエンドを起動し、
// 接続数を数えるカウンタを返す。
func newTestMCPHTTPBackend(t *testing.T) (*httptest.Server, *testConnCounter) {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "echo"}, func(
		_ context.Context, _ *mcp.CallToolRequest, _ map[string]any,
	) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "slow"}, func(
		ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any,
	) (*mcp.CallToolResult, any, error) {
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
		return &mcp.CallToolResult{}, nil, nil
	})
	var conns testConnCounter
	hs := httptest.NewUnstartedServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	))
	hs.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.opened.Add(1)
		}
		if s == http.StateClosed || s == http.StateHijacked {
			conns.closed.Add(1)
		}
	}
	hs.Start()
	t.Cleanup(hs.Close)
	return hs, &conns
}

// TestMCPBackendClient_HTTP_ReusesTransport は、呼び出しを繰り返しても
// コネクションプールが共有され、新規 TCP 接続が増え続けないことを検証する。
func TestMCPBackendClient_HTTP_ReusesTransport(t *testing.T) {
	hs, conns := newTestMCPHTTPBackend(t)
	c := &MCPBackendClient{name: "b", cfg: &config.Server{
		Transport: config.MCPTransportHTTP,
		URL:       hs.URL,
	}}
	t.Cleanup(c.Close)

	for range 10 {
		_, err := c.ListTools(t.Context(), nil)
		require.NoError(t, err)
		_, err = c.CallTool(t.Context(), "echo", []byte(`{}`))
		require.NoError(t, err)
	}
	// 逐次呼び出しなので keep-alive 接続が再利用される（作り直しなら 20 になる）。
	// close 直後の再接続や CI 負荷による揺らぎを許容し、作り直しの半分を上限とする。
	t.Logf("new connections: %d", conns.Load())
	require.LessOrEqual(t, conns.Load(), int32(10))

	first, _ := c.sharedTransport()
	second, _ := c.sharedTransport()
	require.Same(t, first, second)
}

// TestMCPBackendClient_HTTP_CallTimeout は CallTimeout が MCP バックエンド
// 呼び出しに適用されることを検証する。
func TestMCPBackendClient_HTTP_CallTimeout(t *testing.T) {
	hs, _ := newTestMCPHTTPBackend(t)
	c := &MCPBackendClient{name: "b", cfg: &config.Server{
		Transport:   config.MCPTransportHTTP,
		URL:         hs.URL,
		CallTimeout: 200 * time.Millisecond,
	}}
	t.Cleanup(c.Close)

	start := time.Now()
	_, err := c.CallTool(t.Context(), "slow", []byte(`{}`))
	require.Error(t, err)
	require.Less(t, time.Since(start), 3*time.Second)
}

// TestMCPBackendClient_Close_ClosesIdleConnections は Close で共有
// トランスポートのアイドル接続が解放され、以降の接続が拒否されることを検証する。
func TestMCPBackendClient_Close_ClosesIdleConnections(t *testing.T) {
	hs, conns := newTestMCPHTTPBackend(t)
	c := &MCPBackendClient{name: "b", cfg: &config.Server{
		Transport: config.MCPTransportHTTP,
		URL:       hs.URL,
	}}
	_, err := c.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.NotNil(t, c.baseTransport)
	require.Positive(
		t,
		conns.opened.Load()-conns.closed.Load(),
		"keep-alive connection should stay open",
	)

	c.Close()
	// アイドル接続がクライアント側から閉じられ、サーバー側でも終了が観測される。
	require.Eventually(t, func() bool {
		return conns.opened.Load() == conns.closed.Load()
	}, 3*time.Second, 10*time.Millisecond)
	_, err = c.ListTools(t.Context(), nil)
	require.Error(t, err)
}

// TestMCPBackendClient_Stdio_ProcessDetachedFromRequestCtx は、stdio の
// プロセスが接続時のリクエスト ctx のキャンセルで kill されないことを検証する。
func TestMCPBackendClient_Stdio_ProcessDetachedFromRequestCtx(t *testing.T) {
	c := &MCPBackendClient{name: "s", cfg: &config.Server{
		Transport: config.MCPTransportStdio,
		Command:   "sleep",
		Args:      []string{"30"},
	}}
	ctx, cancel := context.WithCancel(t.Context())
	tr, err := c.buildTransport(ctx)
	require.NoError(t, err)
	cancel()

	cmd := tr.(*mcp.CommandTransport).Command //nolint: forcetypeassert
	require.NoError(t, cmd.Start())
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	select {
	case <-done:
		t.Fatal("process was killed by request context cancellation")
	case <-time.After(300 * time.Millisecond):
	}
}
