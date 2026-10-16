package config_test

import (
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestTimeoutsConfig_WithDefaults(t *testing.T) {
	got := config.TimeoutsConfig{}.WithDefaults()
	require.Equal(t, config.DefaultReadHeaderTimeout, got.ReadHeader)
	require.Equal(t, config.DefaultIdleTimeout, got.Idle)
	require.Zero(t, got.Read, "read is unlimited by default (streaming)")
	require.Zero(t, got.Write, "write is unlimited by default (streaming)")

	got = config.TimeoutsConfig{
		ReadHeader: 3 * time.Second, Read: time.Minute, Write: 2 * time.Minute, Idle: -1,
	}.WithDefaults()
	require.Equal(t, 3*time.Second, got.ReadHeader)
	require.Equal(t, time.Minute, got.Read)
	require.Equal(t, 2*time.Minute, got.Write)
	require.Zero(t, got.Idle, "negative disables")
}

func TestGateway_Validate_NegativeMaxRequestBodySize(t *testing.T) {
	g := config.Gateway{MaxRequestBodySize: -1}
	require.Error(t, g.ValidateWithContext(t.Context()))
	g.MaxRequestBodySize = 1024
	require.NoError(t, g.ValidateWithContext(t.Context()))
}
