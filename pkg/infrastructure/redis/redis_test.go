package redis_test

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store/storetest"
	"github.com/stretchr/testify/require"
)

// newTestClient connects to an in-process fake Redis server (miniredis), so
// these tests need no real Redis instance.
func newTestClient(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	c, err := redis.NewClient(t.Context(), &config.RedisConfig{Addrs: []string{mr.Addr()}})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

// TestRedisClient_StoreContract は store.Client の共通契約を redis 実装で検証する。
// go-redis は負の TTL を「期限なし」の SET として送るので、負の TTL で期限切れを作る
// ケース（Get_Expired / Expire_Expired）は対象外。Set_ValueTypes と Get_NotFound の
// エラー文の検証は、もともと redis では検証していなかったので有効にしていない。
func TestRedisClient_StoreContract(t *testing.T) {
	storetest.Run(
		t,
		func(t *testing.T) store.Client { return newTestClient(t) },
		storetest.Options{},
	)
}

func TestKeyPrefix(t *testing.T) {
	ctx := t.Context()
	mr := miniredis.RunT(t)
	c, err := redis.NewClient(ctx, &config.RedisConfig{
		Addrs:     []string{mr.Addr()},
		KeyPrefix: "manifold:",
	})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	require.NoError(t, c.Set(ctx, "k", "v", time.Minute))
	require.True(t, mr.Exists("manifold:k"))
	require.False(t, mr.Exists("k"))

	got, err := c.Get(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, "v", got)

	require.NoError(t, c.Expire(ctx, "k", 2*time.Minute))
	require.Equal(t, 2*time.Minute, mr.TTL("manifold:k"))

	require.NoError(t, c.Del(ctx, "k"))
	require.False(t, mr.Exists("manifold:k"))
	_, err = c.Get(ctx, "k")
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestKeyPrefix_DefaultEmpty(t *testing.T) {
	ctx := t.Context()
	mr := miniredis.RunT(t)
	c, err := redis.NewClient(ctx, &config.RedisConfig{Addrs: []string{mr.Addr()}})
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })

	require.NoError(t, c.Set(ctx, "k", "v", time.Minute))
	require.True(t, mr.Exists("k"))
}
