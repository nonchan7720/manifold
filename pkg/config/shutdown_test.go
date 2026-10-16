package config_test

import (
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestShutdownConfig_WithDefaults(t *testing.T) {
	got := config.ShutdownConfig{}.WithDefaults()
	require.Equal(t, config.DefaultShutdownDrainDelay, got.DrainDelay)
	require.Equal(t, config.DefaultShutdownTimeout, got.Timeout)

	got = config.ShutdownConfig{DrainDelay: -1, Timeout: time.Minute}.WithDefaults()
	require.Zero(t, got.DrainDelay, "negative drainDelay disables the wait")
	require.Equal(t, time.Minute, got.Timeout)

	got = config.ShutdownConfig{DrainDelay: 2 * time.Second}.WithDefaults()
	require.Equal(t, 2*time.Second, got.DrainDelay)
}
