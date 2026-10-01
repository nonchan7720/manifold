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

// newToolMetricsRecorder builds the Recorder shipping tools/call metrics to
// the queue configured in cfg (which must be enabled). topLevelRedis is used
// for type: redis when toolMetrics.redis.client is unset.
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

// toolMetricsMiddlewareFn builds the per-server mcp.Middleware factory
// wiring mcpsrv.NewToolMetricsMiddleware, or nil when rec is nil. The user
// is read from the same header authz uses (authz.headers.userID).
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

// combineMiddlewareFns concatenates the middleware each non-nil fn returns,
// in argument order (so the first fn's middleware is the outermost), or
// returns nil when every fn is nil.
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
