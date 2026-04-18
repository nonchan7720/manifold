package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/stretchr/testify/require"
)

func marshalJSON(v any) ([]byte, error)   { return json.Marshal(v) }
func unmarshalJSON(b []byte, v any) error { return json.Unmarshal(b, v) }

func newTestClient(ctx context.Context, t *testing.T) *sqlite.Client {
	t.Helper()
	c, err := sqlite.NewClient(ctx, ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestNewClient_Memory(t *testing.T) {
	c, err := sqlite.NewClient(t.Context(), ":memory:")
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

func TestDeleteExpired(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Set(ctx, "live", "val", time.Minute))
	require.NoError(t, c.Set(ctx, "dead", "val", -time.Second))

	require.NoError(t, c.DeleteExpired(ctx))

	_, err := c.Get(ctx, "live")
	require.NoError(t, err)

	_, err = c.Get(ctx, "dead")
	require.Error(t, err)
}

func TestStartCleanup(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(ctx, t)

	require.NoError(t, c.Set(ctx, "cleanup_key", "val", -time.Second))

	c.StartCleanup(ctx, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	_, err := c.Get(ctx, "cleanup_key")
	require.Error(t, err)
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
		// []byte を渡した場合、数値列([123 34...])ではなく文字列として保存される
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
