package mcpsrv

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestToolCache_ByteBudget_EvictsLeastRecentlyUsed(t *testing.T) {
	c := newToolCache(100, 10)
	scope := cacheScope{server: "s"}

	require.True(t, c.set(scope, "a", 0, []byte("1234"), time.Minute))
	require.True(t, c.set(scope, "b", 0, []byte("1234"), time.Minute))
	// a を使うと b が最も古くなる。
	_, ok := c.get("a")
	require.True(t, ok)
	require.True(t, c.set(scope, "c", 0, []byte("1234"), time.Minute))

	require.Equal(t, 8, c.Bytes())
	_, ok = c.get("b")
	require.False(t, ok, "least recently used entry is evicted first")
	_, ok = c.get("a")
	require.True(t, ok)
	_, ok = c.get("c")
	require.True(t, ok)
}

func TestToolCache_ValueLargerThanBudget_IsNotStored(t *testing.T) {
	c := newToolCache(100, 4)
	scope := cacheScope{server: "s"}
	require.True(t, c.set(scope, "a", 0, []byte("12"), time.Minute))

	require.False(t, c.set(scope, "big", 0, []byte("12345"), time.Minute))
	require.Equal(t, 1, c.Len())
	require.Equal(t, 2, c.Bytes())
}

func TestToolCache_EntryCap_EvictsLeastRecentlyUsed(t *testing.T) {
	c := newToolCache(2, 1<<20)
	scope := cacheScope{server: "s"}
	require.True(t, c.set(scope, "a", 0, []byte("a"), time.Minute))
	require.True(t, c.set(scope, "b", 0, []byte("b"), time.Minute))
	_, _ = c.get("a")
	require.True(t, c.set(scope, "c", 0, []byte("c"), time.Minute))

	require.Equal(t, 2, c.Len())
	_, ok := c.get("b")
	require.False(t, ok)
	_, ok = c.get("a")
	require.True(t, ok)
}

func TestToolCache_Overwrite_AdjustsBytes(t *testing.T) {
	c := newToolCache(10, 100)
	scope := cacheScope{server: "s"}
	require.True(t, c.set(scope, "a", 0, []byte("1234"), time.Minute))
	require.True(t, c.set(scope, "a", 0, []byte("12"), time.Minute))

	require.Equal(t, 1, c.Len())
	require.Equal(t, 2, c.Bytes())
}

func TestToolCache_Expired_RemovedOnGet(t *testing.T) {
	c := newToolCache(10, 100)
	now := time.Now()
	c.now = func() time.Time { return now }
	scope := cacheScope{server: "s"}
	require.True(t, c.set(scope, "a", 0, []byte("1234"), time.Second))

	now = now.Add(2 * time.Second)
	_, ok := c.get("a")
	require.False(t, ok)
	require.Zero(t, c.Len())
	require.Zero(t, c.Bytes())
}

func TestToolCache_Invalidate_ReleasesBytes(t *testing.T) {
	c := newToolCache(10, 100)
	require.True(t, c.set(cacheScope{server: "s", identity: "u1"}, "a", 0, []byte("12"), time.Minute))
	require.True(t, c.set(cacheScope{server: "s", identity: "u2"}, "b", 0, []byte("123"), time.Minute))

	c.InvalidateCaller("s", "u1")
	require.Equal(t, 1, c.Len())
	require.Equal(t, 3, c.Bytes())

	c.InvalidateServer("s")
	require.Zero(t, c.Len())
	require.Zero(t, c.Bytes())
}

func TestToolCache_ManyEntries_StaysWithinBounds(t *testing.T) {
	c := newToolCache(50, 1000)
	scope := cacheScope{server: "s"}
	for i := range 500 {
		c.set(scope, fmt.Sprintf("k%d", i), 0, []byte(strings.Repeat("x", 30)), time.Minute)
		require.LessOrEqual(t, c.Len(), 50)
		require.LessOrEqual(t, c.Bytes(), 1000)
	}
}
