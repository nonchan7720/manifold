package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/redis/go-redis/v9"
)

// RedisStreamEventField はイベントの JSON を入れるストリームエントリのフィールド名。
const RedisStreamEventField = "event"

// RedisStreamPublisher はイベントを Redis Stream に XADD する。各エントリは
// イベントの JSON を持つフィールド RedisStreamEventField 1 つだけで、バッチ全体を
// パイプラインで 1 往復で送る。受信側は XREAD / XREADGROUP で読む。
type RedisStreamPublisher struct {
	client redis.UniversalClient
	stream string
	maxLen int64
}

var _ toolmetrics.Publisher = (*RedisStreamPublisher)(nil)

// NewRedisStreamPublisher は client を所有する Publisher を作る（Close で client も
// 閉じる）。maxLen が正の値ならストリームを概ねその長さに切り詰める。
func NewRedisStreamPublisher(
	client redis.UniversalClient, stream string, maxLen int64,
) *RedisStreamPublisher {
	return &RedisStreamPublisher{client: client, stream: stream, maxLen: maxLen}
}

func (p *RedisStreamPublisher) Publish(ctx context.Context, events []toolmetrics.Event) error {
	pipe := p.client.Pipeline()
	for _, e := range events {
		body, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal tool metrics event: %w", err)
		}
		args := &redis.XAddArgs{
			Stream: p.stream,
			Values: map[string]any{RedisStreamEventField: body},
		}
		if p.maxLen > 0 {
			args.MaxLen = p.maxLen
			args.Approx = true
		}
		pipe.XAdd(ctx, args)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("redis XADD %s: %w", p.stream, err)
	}
	return nil
}

func (p *RedisStreamPublisher) Close() error { return p.client.Close() }
