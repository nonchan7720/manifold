package sqlite_test

import (
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store/storetest"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *sqlite.Client {
	t.Helper()
	c, err := sqlite.NewClient(t.Context(), ":memory:")
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

// TestSQLiteClient_StoreContract は store.Client の共通契約を sqlite 実装で検証する。
// expires_at は秒単位なので、Expire の検証は既定の TTL（待たずに Get）で行う。
func TestSQLiteClient_StoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Client { return newTestClient(t) }, storetest.Options{
		NegativeTTLExpires: true,
		ValueTypes:         true,
		NotFoundMessage:    true,
	})
}

func TestDeleteExpired(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(t)

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
	c := newTestClient(t)

	require.NoError(t, c.Set(ctx, "cleanup_key", "val", -time.Second))

	c.StartCleanup(ctx, 50*time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	_, err := c.Get(ctx, "cleanup_key")
	require.Error(t, err)
}
