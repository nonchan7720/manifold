package queue

import (
	"encoding/json"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisStreamPublisher_AppendsEventJSON(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	p := NewRedisStreamPublisher(rdb, "tool-metrics", 0)
	t.Cleanup(func() { _ = p.Close() })

	require.NoError(t, p.Publish(t.Context(), events(3)))

	entries, err := mr.Stream("tool-metrics")
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, RedisStreamEventField, entries[0].Values[0])
	var got toolmetrics.Event
	require.NoError(t, json.Unmarshal([]byte(entries[0].Values[1]), &got))
	require.Equal(t, "a", got.ID)
	require.Equal(t, toolmetrics.StatusSuccess, got.Status)
}

func TestRedisStreamPublisher_TrimsToMaxLen(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	p := NewRedisStreamPublisher(rdb, "tool-metrics", 2)
	t.Cleanup(func() { _ = p.Close() })

	require.NoError(t, p.Publish(t.Context(), events(5)))

	entries, err := mr.Stream("tool-metrics")
	require.NoError(t, err)
	// 実 Redis の MAXLEN ~ は概算だが、miniredis は厳密に切り詰める。
	require.Len(t, entries, 2)
}

func TestRedisStreamPublisher_ReturnsErrorWhenUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	p := NewRedisStreamPublisher(rdb, "tool-metrics", 0)
	t.Cleanup(func() { _ = p.Close() })
	mr.Close()

	require.Error(t, p.Publish(t.Context(), events(1)))
}
