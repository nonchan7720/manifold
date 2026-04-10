package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *sqlite.Client {
	t.Helper()
	c, err := sqlite.NewClient(context.Background(), ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestNewClient_Memory(t *testing.T) {
	c, err := sqlite.NewClient(context.Background(), ":memory:")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NoError(t, c.Close())
}

func TestSetAndGet(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	err := c.Set(ctx, "key1", "value1", time.Minute)
	require.NoError(t, err)

	got, err := c.Get(ctx, "key1")
	require.NoError(t, err)
	assert.Equal(t, "value1", got)
}

func TestGet_NotFound(t *testing.T) {
	c := newTestClient(t)

	_, err := c.Get(context.Background(), "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

func TestGet_Expired(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// 過去に期限切れになるTTLで保存
	err := c.Set(ctx, "expiredkey", "val", -time.Second)
	require.NoError(t, err)

	_, err = c.Get(ctx, "expiredkey")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "key not found")
}

func TestSet_Overwrite(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "k", "first", time.Minute))
	require.NoError(t, c.Set(ctx, "k", "second", time.Minute))

	got, err := c.Get(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "second", got)
}

func TestDel(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "delkey", "val", time.Minute))
	require.NoError(t, c.Del(ctx, "delkey"))

	_, err := c.Get(ctx, "delkey")
	require.Error(t, err)
}

func TestDel_NotExisting(t *testing.T) {
	c := newTestClient(t)
	// 存在しないキーの削除はエラーにならない
	err := c.Del(context.Background(), "ghost")
	require.NoError(t, err)
}

func TestDeleteExpired(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	require.NoError(t, c.Set(ctx, "live", "val", time.Minute))
	require.NoError(t, c.Set(ctx, "dead", "val", -time.Second))

	require.NoError(t, c.DeleteExpired(ctx))

	_, err := c.Get(ctx, "live")
	require.NoError(t, err)

	_, err = c.Get(ctx, "dead")
	require.Error(t, err)
}

func TestStartCleanup(t *testing.T) {
	c := newTestClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, c.Set(ctx, "cleanup_key", "val", -time.Second))

	c.StartCleanup(ctx, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	_, err := c.Get(ctx, "cleanup_key")
	require.Error(t, err)
}

func TestImplementsStoreClient(t *testing.T) {
	// store.Client インターフェースを満たすことをコンパイル時に保証するための型アサーション
	c := newTestClient(t)
	var _ interface {
		Set(ctx context.Context, key string, value any, expiration time.Duration) error
		Get(ctx context.Context, key string) (string, error)
		Del(ctx context.Context, key string) error
		Close() error
	} = c
}
