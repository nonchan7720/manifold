package telemetry_test

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/log/noop"
)

func TestNewLoggerProvider_Disabled(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Logs: telemetry.LogsConfig{
			Enabled: false,
		},
	}
	lp, cleanup, err := telemetry.NewLoggerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, lp)
	require.NotNil(t, cleanup)
	defer cleanup()
	_, ok := lp.(noop.LoggerProvider)
	require.True(t, ok)
}

func TestNewLoggerProvider_HTTP(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Logs: telemetry.LogsConfig{
			Enabled: true,
			HTTP: &telemetry.HTTP{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4318",
				},
			},
		},
	}
	lp, cleanup, err := telemetry.NewLoggerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, lp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewLoggerProvider_GRPC(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Logs: telemetry.LogsConfig{
			Enabled: true,
			GRPC: &telemetry.GRPC{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4317",
				},
			},
		},
	}
	lp, cleanup, err := telemetry.NewLoggerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, lp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewLoggerProvider_NoEndpoint(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Logs: telemetry.LogsConfig{
			Enabled: true,
		},
	}
	lp, cleanup, err := telemetry.NewLoggerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, lp)
	require.NotNil(t, cleanup)
	defer cleanup()
	_, ok := lp.(noop.LoggerProvider)
	require.True(t, ok)
}
