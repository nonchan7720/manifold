package mcpsrv

import (
	"context"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// --- buildTransport ---

func TestBuildTransport_StdioEmptyCommand(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportStdio,
			Command:   "", // 空のコマンド
		},
	}

	_, err := c.buildTransport(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "command is required")
}

func TestBuildTransport_UnknownTransport(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: "unknown",
		},
	}

	_, err := c.buildTransport(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown transport")
}

func TestBuildTransport_HTTP_NoAuthValue(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

func TestBuildTransport_HTTP_WithAuthValue(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
			AuthValue: &config.AuthValue{
				Header: "Authorization",
				Prefix: "Bearer",
				Value:  "static-token",
			},
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

func TestBuildTransport_Stdio_WithCommand(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportStdio,
			Command:   "/bin/echo",
			Args:      []string{"hello"},
			Env:       map[string]string{"TEST_VAR": "value"},
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

// --- MCPBackendClient.Close ---

func TestMCPBackendClient_Close_NotConnected(t *testing.T) {
	c := &MCPBackendClient{
		name:      "test",
		cfg:       &config.Server{},
		connected: false,
		session:   nil,
	}
	// 接続していない場合はパニックしない
	require.NotPanics(t, func() {
		c.Close()
	})
	require.False(t, c.connected)
}
