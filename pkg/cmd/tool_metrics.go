package cmd

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/aws"
	"github.com/nonchan7720/manifold/pkg/infrastructure/queue"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	"github.com/nonchan7720/manifold/pkg/services/toolmetrics"
)

// newToolMetricsRecorder は cfg で設定されたキューへ tools/call のメトリクスを
// 送る Recorder を作る（cfg は有効であること）。type: redis で
// toolMetrics.redis.client が未設定の場合は topLevelRedis に接続する。
func newToolMetricsRecorder(
	ctx context.Context, cfg config.ToolMetricsConfig, topLevelRedis *config.RedisConfig,
) (*toolmetrics.Recorder, error) {
	cfg = cfg.WithDefaults()

	var pub toolmetrics.Publisher
	switch cfg.Type {
	case config.ToolMetricsTypeSQS:
		awsCfg, err := aws.NewConfig(ctx)
		if err != nil {
			return nil, fmt.Errorf("tool metrics: load aws config: %w", err)
		}
		pub = queue.NewSQSPublisher(
			sqs.NewFromConfig(awsCfg), cfg.SQS.QueueURL, cfg.SQS.MessageGroupID,
		)
	case config.ToolMetricsTypeRedis:
		redisCfg := cfg.Redis.Client
		if redisCfg == nil {
			redisCfg = topLevelRedis
		}
		rdb, err := redis.NewUniversalClient(ctx, redisCfg)
		if err != nil {
			return nil, fmt.Errorf("tool metrics: %w", err)
		}
		pub = queue.NewRedisStreamPublisher(rdb, cfg.Redis.Stream, cfg.Redis.MaxLen)
	default:
		return nil, fmt.Errorf("tool metrics: unsupported type %q", cfg.Type)
	}

	return toolmetrics.NewRecorder(ctx, pub, toolmetrics.RecorderOptions{
		BufferSize:     cfg.BufferSize,
		BatchSize:      cfg.BatchSize,
		FlushInterval:  cfg.FlushInterval,
		PublishTimeout: cfg.PublishTimeout,
	}), nil
}

// toolMetricsMiddlewareFn は mcpsrv.NewToolMetricsMiddleware をサーバーごとに
// 組み込む mcp.Middleware のファクトリを返す。rec が nil なら nil を返す。
// ユーザーは authz と同じヘッダー（authz.headers.userID）から読む。
func toolMetricsMiddlewareFn(
	rec *toolmetrics.Recorder, authzCfg config.AuthzConfig, servers config.Servers,
) func(name string) []mcp.Middleware {
	if rec == nil {
		return nil
	}
	userHeader := authzCfg.WithDefaults().Headers.UserID
	return func(name string) []mcp.Middleware {
		return []mcp.Middleware{
			mcpsrv.NewToolMetricsMiddleware(name, serviceCodeOf(servers, name), userHeader, rec),
		}
	}
}

// combineMiddlewareFns は nil でない各 fn が返すミドルウェアを引数の順に連結する
// （先頭の fn のミドルウェアが最も外側になる）。すべて nil なら nil を返す。
func combineMiddlewareFns(
	fns ...func(name string) []mcp.Middleware,
) func(name string) []mcp.Middleware {
	var active []func(name string) []mcp.Middleware
	for _, fn := range fns {
		if fn != nil {
			active = append(active, fn)
		}
	}
	if len(active) == 0 {
		return nil
	}
	return func(name string) []mcp.Middleware {
		var mws []mcp.Middleware
		for _, fn := range active {
			mws = append(mws, fn(name)...)
		}
		return mws
	}
}
