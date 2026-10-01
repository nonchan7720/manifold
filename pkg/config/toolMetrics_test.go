package config

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestToolMetricsConfig_DisabledSkipsValidation(t *testing.T) {
	require.NoError(t, ToolMetricsConfig{Type: "kafka"}.ValidateWithContext(context.Background()))
}

func TestToolMetricsConfig_WithDefaults(t *testing.T) {
	got := ToolMetricsConfig{Redis: &ToolMetricsRedis{}}.WithDefaults()
	require.Equal(t, DefaultToolMetricsBufferSize, got.BufferSize)
	require.Equal(t, DefaultToolMetricsBatchSize, got.BatchSize)
	require.Equal(t, DefaultToolMetricsFlushInterval, got.FlushInterval)
	require.Equal(t, DefaultToolMetricsPublishTimeout, got.PublishTimeout)
	require.Equal(t, DefaultToolMetricsRedisStream, got.Redis.Stream)
}

func TestToolMetricsConfig_Validate(t *testing.T) {
	topLevel := &RedisConfig{URL: "redis://localhost:6379"}
	tests := []struct {
		name     string
		cfg      ToolMetricsConfig
		topRedis *RedisConfig
		wantErr  string
	}{
		{
			name: "sqs ok",
			cfg: ToolMetricsConfig{Enabled: true, Type: ToolMetricsTypeSQS,
				SQS: &ToolMetricsSQS{QueueURL: "https://sqs.example/q"}},
		},
		{
			name: "sqs without queueURL",
			cfg: ToolMetricsConfig{
				Enabled: true,
				Type:    ToolMetricsTypeSQS,
				SQS:     &ToolMetricsSQS{},
			},
			wantErr: "QueueURL: cannot be blank",
		},
		{
			name:    "sqs block missing",
			cfg:     ToolMetricsConfig{Enabled: true, Type: ToolMetricsTypeSQS},
			wantErr: "SQS: is required",
		},
		{
			name: "redis falls back to top-level redis",
			cfg: ToolMetricsConfig{
				Enabled: true,
				Type:    ToolMetricsTypeRedis,
				Redis:   &ToolMetricsRedis{},
			},
			topRedis: topLevel,
		},
		{
			name: "redis with own client",
			cfg: ToolMetricsConfig{Enabled: true, Type: ToolMetricsTypeRedis,
				Redis: &ToolMetricsRedis{Client: topLevel}},
		},
		{
			name: "redis without any connection",
			cfg: ToolMetricsConfig{
				Enabled: true,
				Type:    ToolMetricsTypeRedis,
				Redis:   &ToolMetricsRedis{},
			},
			wantErr: "client is required",
		},
		{
			name:    "unknown type",
			cfg:     ToolMetricsConfig{Enabled: true, Type: "kafka"},
			wantErr: "Type",
		},
		{
			name:    "missing type",
			cfg:     ToolMetricsConfig{Enabled: true},
			wantErr: "Type",
		},
		{
			name: "negative flushInterval",
			cfg: ToolMetricsConfig{Enabled: true, Type: ToolMetricsTypeSQS,
				SQS: &ToolMetricsSQS{QueueURL: "q"}, FlushInterval: -time.Second},
			wantErr: "FlushInterval",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), redisContextKey{}, tt.topRedis)
			err := tt.cfg.ValidateWithContext(ctx)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}
