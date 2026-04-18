package contexts

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestHeaderContext(t *testing.T) {
	ctx := context.Background()

	// 初期状態はnil
	v := FromHeaderContext(ctx)
	require.Nil(t, v)

	// set and get
	headers := map[string][]string{
		"X-Test":    {"value1"},
		"X-Another": {"value2", "value3"},
	}
	ctx = ToHeaderContext(ctx, headers)
	got := FromHeaderContext(ctx)
	require.Equal(t, headers, got)
}

func TestRequestAuthHeader(t *testing.T) {
	ctx := context.Background()

	// 初期状態は空文字
	v := FromRequestAuthHeader(ctx)
	require.Empty(t, v)

	// set and get
	token := "Bearer test-token-abc"
	ctx = ToRequestAuthHeader(ctx, token)
	got := FromRequestAuthHeader(ctx)
	require.Equal(t, token, got)
}

func TestServerContext(t *testing.T) {
	ctx := context.Background()

	// 初期状態はnil
	v := FromServerContext(ctx)
	require.Nil(t, v)

	// set and get
	srv := &config.Server{
		Name:    "test-server",
		BaseURL: "http://example.com",
	}
	ctx = ToServerContext(ctx, srv)
	got := FromServerContext(ctx)
	require.Equal(t, srv, got)
	require.Equal(t, "test-server", got.Name)
}

func TestContextsIndependence(t *testing.T) {
	// 各コンテキストキーが独立していることを確認
	ctx := context.Background()
	ctx = ToRequestAuthHeader(ctx, "Bearer token")
	ctx = ToHeaderContext(ctx, map[string][]string{"X-Foo": {"bar"}})
	ctx = ToServerContext(ctx, &config.Server{Name: "srv"})

	require.Equal(t, "Bearer token", FromRequestAuthHeader(ctx))
	require.Equal(t, map[string][]string{"X-Foo": {"bar"}}, FromHeaderContext(ctx))
	require.Equal(t, "srv", FromServerContext(ctx).Name)
}
