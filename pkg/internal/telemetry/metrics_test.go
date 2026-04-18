package telemetry_test

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewMeterProvider_Disabled(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Metrics: telemetry.MetricsConfig{
			Enabled: false,
		},
	}
	mp, handler, cleanup, err := telemetry.NewMeterProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.Nil(t, handler)
	require.NotNil(t, cleanup)
	defer cleanup()
	_, ok := mp.(noop.MeterProvider)
	require.True(t, ok)
}

func TestNewMeterProvider_Pull(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Metrics: telemetry.MetricsConfig{
			Enabled:      true,
			ExporterType: telemetry.ExporterTypePull,
		},
	}
	mp, handler, cleanup, err := telemetry.NewMeterProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.NotNil(t, handler)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewMeterProvider_Push_HTTP(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Metrics: telemetry.MetricsConfig{
			Enabled:      true,
			ExporterType: telemetry.ExporterTypePush,
			HTTP: &telemetry.HTTP{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4318",
				},
			},
		},
	}
	mp, handler, cleanup, err := telemetry.NewMeterProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.Nil(t, handler)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewMeterProvider_Push_GRPC(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Metrics: telemetry.MetricsConfig{
			Enabled:      true,
			ExporterType: telemetry.ExporterTypePush,
			GRPC: &telemetry.GRPC{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4317",
				},
			},
		},
	}
	mp, handler, cleanup, err := telemetry.NewMeterProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.Nil(t, handler)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewMeterProvider_Push_NoEndpoint(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Metrics: telemetry.MetricsConfig{
			Enabled:      true,
			ExporterType: telemetry.ExporterTypePush,
		},
	}
	mp, handler, cleanup, err := telemetry.NewMeterProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, mp)
	require.Nil(t, handler)
	require.NotNil(t, cleanup)
	defer cleanup()
	_, ok := mp.(noop.MeterProvider)
	require.True(t, ok)
}
