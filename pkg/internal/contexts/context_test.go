package contexts

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/assert"
)

func TestHeaderContext(t *testing.T) {
	ctx := context.Background()

	// 初期状態はnil
	v := FromHeaderContext(ctx)
	assert.Nil(t, v)

	// set and get
	headers := map[string][]string{
		"X-Test":    {"value1"},
		"X-Another": {"value2", "value3"},
	}
	ctx = ToHeaderContext(ctx, headers)
	got := FromHeaderContext(ctx)
	assert.Equal(t, headers, got)
}

func TestRequestAuthHeader(t *testing.T) {
	ctx := context.Background()

	// 初期状態は空文字
	v := FromRequestAuthHeader(ctx)
	assert.Empty(t, v)

	// set and get
	token := "Bearer test-token-abc"
	ctx = ToRequestAuthHeader(ctx, token)
	got := FromRequestAuthHeader(ctx)
	assert.Equal(t, token, got)
}

func TestServerContext(t *testing.T) {
	ctx := context.Background()

	// 初期状態はnil
	v := FromServerContext(ctx)
	assert.Nil(t, v)

	// set and get
	srv := &config.Server{
		Name:    "test-server",
		BaseURL: "http://example.com",
	}
	ctx = ToServerContext(ctx, srv)
	got := FromServerContext(ctx)
	assert.Equal(t, srv, got)
	assert.Equal(t, "test-server", got.Name)
}

func TestContextsIndependence(t *testing.T) {
	// 各コンテキストキーが独立していることを確認
	ctx := context.Background()
	ctx = ToRequestAuthHeader(ctx, "Bearer token")
	ctx = ToHeaderContext(ctx, map[string][]string{"X-Foo": {"bar"}})
	ctx = ToServerContext(ctx, &config.Server{Name: "srv"})

	assert.Equal(t, "Bearer token", FromRequestAuthHeader(ctx))
	assert.Equal(t, map[string][]string{"X-Foo": {"bar"}}, FromHeaderContext(ctx))
	assert.Equal(t, "srv", FromServerContext(ctx).Name)
}
