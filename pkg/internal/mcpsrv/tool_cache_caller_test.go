package mcpsrv

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

func TestCacheCaller_Identifiable(t *testing.T) {
	base := t.Context()
	static := domainedge.WithIdentityKey(base, domainedge.StaticIdentityKey)

	tests := []struct {
		name string
		ctx  func() context.Context
		want bool
	}{
		{"nothing", func() context.Context { return base }, false},
		{
			"token",
			func() context.Context { return contexts.ToRequestAuthHeader(base, "Bearer a") },
			true,
		},
		{
			"remote identity",
			func() context.Context { return domainedge.WithIdentityKey(base, "oauth:user-a") },
			true,
		},
		// static pairing: 全 HTTP クライアントが同じ identityKey を共有する。
		{"static identity only", func() context.Context { return static }, false},
		{
			"static identity with token",
			func() context.Context { return contexts.ToRequestAuthHeader(static, "Bearer a") },
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := cacheCaller(tt.ctx())
			require.Equal(t, tt.want, ok)
		})
	}
}

func TestToolCacheMiddleware_StaticIdentity_BypassesCache(t *testing.T) {
	cfg := &config.CacheConfig{ToolCall: time.Minute, Tools: []string{"echo"}}
	cache := NewToolCache(0)
	mw := newToolCacheMiddleware("web", cfg, cache)
	require.NotNil(t, mw)

	calls := 0
	handler := mw(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		calls++
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "result"}},
		}, nil
	})
	req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "echo"}}
	call := func(ctx context.Context) {
		t.Helper()
		_, err := handler(ctx, authzMethodToolsCall, req)
		require.NoError(t, err)
	}

	static := domainedge.WithIdentityKey(t.Context(), domainedge.StaticIdentityKey)
	call(static)
	call(static)
	require.Equal(t, 2, calls, "static identity without a token is never served from the cache")
	require.Zero(t, cache.Len(), "...nor stored")

	// 区別できる呼び出し元は従来どおりキャッシュされる。
	remote := domainedge.WithIdentityKey(t.Context(), "oauth:user-a")
	call(remote)
	call(remote)
	require.Equal(t, 3, calls)
	require.Equal(t, 1, cache.Len())
}

func TestReverseGateway_Init_StaticPairingWithCache_WarnsCacheIgnored(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	servers := staticReverseServers()
	servers["app1"].Cache = &config.CacheConfig{ToolsList: time.Minute}
	newTestReverseGateway(t, servers, staticEdgeConfig())
	require.Contains(t, buf.String(), "cache is ignored on a static pairing reverse server")
	require.Contains(t, buf.String(), "app1")

	buf.Reset()
	newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	require.NotContains(t, buf.String(), "cache is ignored")
}
