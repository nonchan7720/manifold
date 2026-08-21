package mcpsrv

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func newConnectedToolPair(t *testing.T) (*mcp.ClientSession, []*mcp.Tool) {
	t.Helper()
	backend := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	backend.AddTool(
		&mcp.Tool{
			Name:        "greet",
			Description: "greet",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}}, nil
		},
	)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := backend.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "manifold", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	result, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	return session, result.Tools
}

func TestRegisterSessionTools_ForwardsCallToResolvedSession(t *testing.T) {
	backendSession, tools := newConnectedToolPair(t)

	front := mcp.NewServer(&mcp.Implementation{Name: "front", Version: "0.0.1"}, nil)
	RegisterSessionTools(front, tools, func(context.Context) (*mcp.ClientSession, error) {
		return backendSession, nil
	})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := front.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "greet"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "hello", text.Text)
}

func TestRegisterSessionTools_ResolveError_ReturnsToolError(t *testing.T) {
	_, tools := newConnectedToolPair(t)

	front := mcp.NewServer(&mcp.Implementation{Name: "front", Version: "0.0.1"}, nil)
	resolveErr := errors.New("no live tab")
	RegisterSessionTools(front, tools, func(context.Context) (*mcp.ClientSession, error) {
		return nil, resolveErr
	})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err := front.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "greet"})
	require.NoError(t, err)
	require.True(t, result.IsError)
}
