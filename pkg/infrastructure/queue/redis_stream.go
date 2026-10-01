package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/redis/go-redis/v9"
)

// RedisStreamEventField is the stream entry field holding the event JSON.
const RedisStreamEventField = "event"

// RedisStreamPublisher appends each event to a Redis Stream (XADD) as a
// single field RedisStreamEventField holding the event's JSON, pipelining a
// whole batch in one round trip. Consumers read it with XREAD/XREADGROUP.
type RedisStreamPublisher struct {
	client redis.UniversalClient
	stream string
	maxLen int64
}

var _ toolmetrics.Publisher = (*RedisStreamPublisher)(nil)

// NewRedisStreamPublisher builds a publisher that owns client (Close closes
// it). A positive maxLen trims the stream approximately to that length.
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
