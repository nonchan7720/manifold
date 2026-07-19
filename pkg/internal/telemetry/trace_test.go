package telemetry_test

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestNewTracerProvider_Disabled(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: false,
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
	_, ok := tp.(noop.TracerProvider)
	require.True(t, ok)
}

func TestNewTracerProvider_NoHTTPOrGRPC(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
	_, ok := tp.(noop.TracerProvider)
	require.True(t, ok)
}

func TestNewTracerProvider_HTTP(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			HTTP: &telemetry.HTTP{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4318",
				},
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewTracerProvider_HTTP_WithEndpointURL(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			HTTP: &telemetry.HTTP{
				Endpoint: telemetry.Endpoint{
					EndpointURL: "http://localhost:4318",
				},
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewTracerProvider_HTTP_NoEndpoint(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			HTTP:    &telemetry.HTTP{},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.Error(t, err)
	require.Nil(t, tp)
	require.Nil(t, cleanup)
}

func TestNewTracerProvider_HTTP_GzipCompression(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		GzipCompression: true,
		Trace: telemetry.Trace{
			Enabled: true,
			HTTP: &telemetry.HTTP{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4318",
				},
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewTracerProvider_GRPC(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			GRPC: &telemetry.GRPC{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4317",
				},
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewTracerProvider_GRPC_WithEndpointURL(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			GRPC: &telemetry.GRPC{
				Endpoint: telemetry.Endpoint{
					EndpointURL: "http://localhost:4317",
				},
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewTracerProvider_GRPC_Insecure(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			GRPC: &telemetry.GRPC{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4317",
				},
				Insecure: true,
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}

func TestNewTracerProvider_GRPC_NoEndpoint(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		Trace: telemetry.Trace{
			Enabled: true,
			GRPC:    &telemetry.GRPC{},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.Error(t, err)
	require.Nil(t, tp)
	require.Nil(t, cleanup)
}

func TestNewTracerProvider_GRPC_GzipCompression(t *testing.T) {
	ctx := context.Background()
	cfg := &telemetry.Config{
		GzipCompression: true,
		Trace: telemetry.Trace{
			Enabled: true,
			GRPC: &telemetry.GRPC{
				Endpoint: telemetry.Endpoint{
					Endpoint: "localhost:4317",
				},
			},
		},
	}
	tp, cleanup, err := telemetry.NewTracerProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, tp)
	require.NotNil(t, cleanup)
	defer cleanup()
}
