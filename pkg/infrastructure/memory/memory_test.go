package memory_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store/storetest"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T) *memory.Client {
	t.Helper()
	c, err := memory.NewClient(t.Context())
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

// TestMemoryClient_StoreContract は store.Client の共通契約を memory 実装で検証する。
func TestMemoryClient_StoreContract(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Client { return newTestClient(t) }, storetest.Options{
		NegativeTTLExpires: true,
		ValueTypes:         true,
		NotFoundMessage:    true,
		// memory は実時計をナノ秒精度で見るので、最初の TTL が切れた後も値が残ることまで確かめる。
		ExpireTTL: storetest.ExpireTTL{
			Initial:  time.Millisecond,
			Extended: time.Minute,
			Wait:     5 * time.Millisecond,
		},
	})
}

func TestConcurrentAccess(t *testing.T) {
	ctx := t.Context()
	c := newTestClient(t)

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
