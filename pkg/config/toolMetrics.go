package config

import (
	"context"
	"fmt"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// ツールメトリクスの送信先キューの種別。
const (
	ToolMetricsTypeSQS   = "sqs"
	ToolMetricsTypeRedis = "redis"
)

// 既定値（toolMetrics の各フィールド省略時）。
const (
	DefaultToolMetricsBufferSize     = 1024
	DefaultToolMetricsBatchSize      = 10
	DefaultToolMetricsFlushInterval  = time.Second
	DefaultToolMetricsPublishTimeout = 5 * time.Second
	DefaultToolMetricsRedisStream    = "manifold:tool-metrics"
)

// ToolMetricsConfig は tools/call ごとのメトリクスイベント（呼び出し回数・
// ステータス・エラーメッセージ等）をキューサービスへ非同期送信する設定。
// 既定では無効（Enabled: false）。
type ToolMetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Type    string `mapstructure:"type"`

	// BufferSize はキュー送信待ちイベントを溜めるメモリ上のバッファ長。
	// 満杯の間に発生したイベントはツール呼び出しを遅らせないよう破棄する。
	BufferSize int `mapstructure:"bufferSize"`
	// BatchSize は 1 回の送信にまとめる最大イベント数。
	BatchSize int `mapstructure:"batchSize"`
	// FlushInterval は BatchSize に満たなくても溜まったイベントを送信する間隔。
	FlushInterval time.Duration `mapstructure:"flushInterval"`
	// PublishTimeout は 1 回の送信のタイムアウト。
	PublishTimeout time.Duration `mapstructure:"publishTimeout"`

	SQS   *ToolMetricsSQS   `mapstructure:"sqs"`
	Redis *ToolMetricsRedis `mapstructure:"redis"`
}

// ToolMetricsSQS は Amazon SQS への送信設定。認証情報・リージョンは
// storage.s3 と同じく AWS SDK の既定の解決順（環境変数・共有設定・IAM ロール）に従う。
type ToolMetricsSQS struct {
	QueueURL string `mapstructure:"queueURL"`
	// MessageGroupID は FIFO キュー（.fifo）の場合に設定する。
	// 設定時は MessageDeduplicationId にイベント ID を使う。
	MessageGroupID string `mapstructure:"messageGroupID"`
}

// ToolMetricsRedis は Redis Streams（XADD）への送信設定。
type ToolMetricsRedis struct {
	Stream string `mapstructure:"stream"`
	// MaxLen が正の値ならストリームを概ねこの長さに保つ（XADD MAXLEN ~）。
	MaxLen int64 `mapstructure:"maxLen"`
	// Client は接続先。省略時はトップレベルの redis 設定を使う。
	Client *RedisConfig `mapstructure:"client"`
}

// WithDefaults returns a copy of c with zero-value fields replaced by the
// documented defaults.
func (c ToolMetricsConfig) WithDefaults() ToolMetricsConfig {
	if c.BufferSize == 0 {
		c.BufferSize = DefaultToolMetricsBufferSize
	}
	if c.BatchSize == 0 {
		c.BatchSize = DefaultToolMetricsBatchSize
	}
	if c.FlushInterval == 0 {
		c.FlushInterval = DefaultToolMetricsFlushInterval
	}
	if c.PublishTimeout == 0 {
		c.PublishTimeout = DefaultToolMetricsPublishTimeout
	}
	if c.Redis != nil && c.Redis.Stream == "" {
		redis := *c.Redis
		redis.Stream = DefaultToolMetricsRedisStream
		c.Redis = &redis
	}
	return c
}

// redisContextKey carries the top-level Config.Redis into ToolMetricsConfig
// validation, so type: redis can fall back to it when redis.client is unset.
type redisContextKey struct{}

func (c ToolMetricsConfig) ValidateWithContext(ctx context.Context) error {
	if !c.Enabled {
		return nil
	}
	c = c.WithDefaults()
	topLevelRedis, _ := ctx.Value(redisContextKey{}).(*RedisConfig)
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Type,
			validation.Required,
			validation.In(ToolMetricsTypeSQS, ToolMetricsTypeRedis),
		),
		validation.Field(&c.BufferSize, validation.Min(1)),
		validation.Field(&c.BatchSize, validation.Min(1)),
		validation.Field(&c.FlushInterval, validation.By(validatePositiveDuration)),
		validation.Field(&c.PublishTimeout, validation.By(validatePositiveDuration)),
		validation.Field(&c.SQS,
			validation.When(c.Type == ToolMetricsTypeSQS, validation.NotNil),
		),
		validation.Field(&c.Redis,
			validation.When(c.Type == ToolMetricsTypeRedis,
				validation.NotNil,
				validation.By(func(any) error {
					if c.Redis != nil && c.Redis.Client == nil && topLevelRedis == nil {
						return fmt.Errorf("client is required when the top-level redis is not set")
					}
					return nil
				}),
			),
		),
	)
}

func (c *ToolMetricsSQS) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, c,
		validation.Field(&c.QueueURL, validation.Required),
	)
}

func (c *ToolMetricsRedis) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, c,
		validation.Field(&c.MaxLen, validation.Min(int64(0))),
	)
}
