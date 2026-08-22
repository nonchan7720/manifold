package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
	"github.com/stretchr/testify/require"
)

// newTestClient connects to an in-process fake Redis server (miniredis), so
// these tests need no real Redis instance.
func newTestClient(ctx context.Context, t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := redis.NewClient(ctx, &config.RedisConfig{Addrs: []string{mr.Addr()}})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSetAndGet(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Set(ctx, "key1", "value1", time.Minute))

	got, err := c.Get(ctx, "key1")
	require.NoError(t, err)
	require.Equal(t, "value1", got)
}

func TestGet_NotFound(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	_, err := c.Get(ctx, "nonexistent")
	require.Error(t, err)
}

func TestSet_Overwrite(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Set(ctx, "k", "first", time.Minute))
	require.NoError(t, c.Set(ctx, "k", "second", time.Minute))

	got, err := c.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "second", got)
}

func TestDel(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Set(ctx, "delkey", "val", time.Minute))
	require.NoError(t, c.Del(ctx, "delkey"))

	_, err := c.Get(ctx, "delkey")
	require.Error(t, err)
}

func TestDel_NotExisting(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Del(ctx, "ghost"))
}

func TestExpire_UpdatesTTLWithoutChangingValue(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Set(ctx, "k", "value1", time.Minute))
	require.NoError(t, c.Expire(ctx, "k", time.Hour))

	got, err := c.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "value1", got, "Expire must not change the stored value")
}

func TestExpire_NotFound(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	err := c.Expire(ctx, "nonexistent", time.Minute)
	require.Error(t, err)
	require.Contains(t, err.Error(), "key not found")
}

func TestImplementsStoreClient(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)
	var _ interface {
		Set(ctx context.Context, key string, value any, expiration time.Duration) error
		Get(ctx context.Context, key string) (string, error)
		Del(ctx context.Context, key string) error
		Expire(ctx context.Context, key string, expiration time.Duration) error
		Close() error
	} = c
}
