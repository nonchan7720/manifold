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
	// RedisStreamMessageIDField は本文の message_id。受信側が本文を解析せずに
	// 重複（再送・再配信）を判定できるようにする。
	RedisStreamMessageIDField = "messageId"
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
				RedisStreamMessageIDField, e.GetMessageId(),
			},
		}
		if p.maxLen > 0 {
			args.MaxLen = p.maxLen
			args.Approx = true
		}
		pipe.XAdd(ctx, args)
	}
	// go-redis は既定（ContextTimeoutEnabled: false）では ctx のキャンセルや期限で
	// 応答の読み取りを中断せず、ReadTimeout まで待つ。publishTimeout と Close の
	// 打ち切りを効かせるため別 goroutine で実行し、ctx が終わったら待たずに戻る。
	// 置き去りの読み取りは Close でクライアントを閉じると解除される。戻った後に
	// XADD が成功していることはあるが、配送は at-least-once で受信側は messageId で
	// 重複を判定するので問題ない。
	errCh := make(chan error, 1)
	go func() {
		_, err := pipe.Exec(ctx)
		errCh <- err
	}()
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("redis XADD %s: %w", p.stream, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("redis XADD %s: %w", p.stream, ctx.Err())
	}
}

func (p *RedisStreamPublisher) Close() error { return p.client.Close() }
