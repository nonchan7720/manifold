package queue

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
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
	require.Equal(t, []string{
		RedisStreamEventField, entries[0].Values[1],
		RedisStreamSchemaField, "manifold.toolmetrics.v1.ToolCallEvent",
		RedisStreamContentTypeField, toolmetrics.ContentType,
		RedisStreamMessageIDField, "a",
	}, entries[0].Values)
	got := &toolmetrics.Event{}
	require.NoError(t, protojson.Unmarshal([]byte(entries[0].Values[1]), got))
	require.Equal(t, "a", got.GetMessageId())
	require.Equal(t, toolmetrics.StatusSuccess, got.GetStatus())
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

func TestRedisStreamPublisher_ReturnsWhenContextEndsEvenIfRedisHangs(t *testing.T) {
	// 接続を受け付けるが何も応答しないサーバー（応答しない Redis の再現）
	var lc net.ListenConfig
	l, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()

	rdb := redis.NewClient(&redis.Options{
		Addr:        l.Addr().String(),
		ReadTimeout: time.Minute, // ctx が効かなければ 1 分待つ
		MaxRetries:  -1,
	})
	p := NewRedisStreamPublisher(rdb, "tool-metrics", 0)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = p.Publish(ctx, events(1))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 5*time.Second)

	// Close でクライアントを閉じれば、置き去りの読み取りも解除される
	require.NoError(t, p.Close())
}
