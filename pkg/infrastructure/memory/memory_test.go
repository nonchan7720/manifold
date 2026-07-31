package memory_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/stretchr/testify/require"
)

func marshalJSON(v any) ([]byte, error)   { return json.Marshal(v) }
func unmarshalJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

func newTestClient(ctx context.Context, t *testing.T) *memory.Client {
	t.Helper()
	c, err := memory.NewClient(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestNewClient(t *testing.T) {
	c, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NoError(t, c.Close())
}

func TestSetAndGet(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	err := c.Set(ctx, "key1", "value1", time.Minute)
	require.NoError(t, err)

	got, err := c.Get(ctx, "key1")
	require.NoError(t, err)
	require.Equal(t, "value1", got)
}

func TestGet_NotFound(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	_, err := c.Get(ctx, "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "key not found")
}

func TestGet_Expired(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	// 過去に期限切れになるTTLで保存
	err := c.Set(ctx, "expiredkey", "val", -time.Second)
	require.NoError(t, err)

	_, err = c.Get(ctx, "expiredkey")
	require.Error(t, err)
	require.Contains(t, err.Error(), "key not found")
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
	// 存在しないキーの削除はエラーにならない
	err := c.Del(context.Background(), "ghost")
	require.NoError(t, err)
}

func TestSet_ValueTypes(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		ctx := t.Context()
		c := newTestClient(ctx, t)
		require.NoError(t, c.Set(ctx, "k", "hello", time.Minute))
		got, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, "hello", got)
	})

	t.Run("[]byte JSON", func(t *testing.T) {
		ctx := t.Context()
		c := newTestClient(ctx, t)
		raw := []byte(`{"token":"abc","expires":3600}`)
		require.NoError(t, c.Set(ctx, "k", raw, time.Minute))
		got, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, string(raw), got)
	})

	t.Run("[]byte roundtrip via json.Unmarshal", func(t *testing.T) {
		ctx := t.Context()
		c := newTestClient(ctx, t)
		type payload struct {
			Token   string `json:"token"`
			Expires int    `json:"expires"`
		}
		in := payload{Token: "abc", Expires: 3600}
		raw, err := marshalJSON(in)
		require.NoError(t, err)

		require.NoError(t, c.Set(ctx, "k", raw, time.Minute))

		got, err := c.Get(ctx, "k")
		require.NoError(t, err)

		var out payload
		require.NoError(t, unmarshalJSON([]byte(got), &out))
		require.Equal(t, in, out)
	})
}

func TestConcurrentAccess(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = c.Set(ctx, "k", i, time.Minute)
			_, _ = c.Get(ctx, "k")
		}(i)
	}
	wg.Wait()
}

func TestImplementsStoreClient(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)
	var _ interface {
		Set(ctx context.Context, key string, value any, expiration time.Duration) error
		Get(ctx context.Context, key string) (string, error)
		Del(ctx context.Context, key string) error
		Close() error
	} = c
}
