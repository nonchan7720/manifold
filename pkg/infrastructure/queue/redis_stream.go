package queue

import (
	"context"
	"fmt"

	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
	"github.com/redis/go-redis/v9"
)

// ストリームエントリのフィールド名。
const (
	// RedisStreamEventField はイベントの protojson を入れるフィールド。
	RedisStreamEventField = "event"
	// RedisStreamSchemaField は本文の型の完全修飾名（toolmetrics.SchemaName）。
	RedisStreamSchemaField = "schema"
	// RedisStreamContentTypeField は本文の形式（toolmetrics.ContentType）。
	RedisStreamContentTypeField = "contentType"
)

// RedisStreamPublisher はイベントを Redis Stream に XADD する。各エントリは
// イベントの protojson（RedisStreamEventField）と、そのスキーマ名・形式を持つ。
// バッチ全体をパイプラインで 1 往復で送る。受信側は XREAD / XREADGROUP で読む。
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

func (p *RedisStreamPublisher) Publish(ctx context.Context, events []*toolmetrics.Event) error {
	pipe := p.client.Pipeline()
	for _, e := range events {
		body, err := toolmetrics.Marshal(e)
		if err != nil {
			return fmt.Errorf("marshal tool metrics event: %w", err)
		}
		args := &redis.XAddArgs{
			Stream: p.stream,
			// フィールドの順序を固定するためスライスで渡す。
			Values: []any{
				RedisStreamEventField, body,
				RedisStreamSchemaField, toolmetrics.SchemaName,
				RedisStreamContentTypeField, toolmetrics.ContentType,
			},
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
