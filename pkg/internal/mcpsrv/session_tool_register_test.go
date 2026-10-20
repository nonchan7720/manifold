package mcpsrv

import (
	"context"
	"encoding/json"
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
	RegisterSessionTools(t.Context(), front, tools,
		func(context.Context) (*mcp.ClientSession, error) {
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
	RegisterSessionTools(t.Context(), front, tools,
		func(context.Context) (*mcp.ClientSession, error) {
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

// TestRegisterSessionTools_InvalidDefinition_SkippedWithoutPanic covers tool
// definitions a tab can send that (*mcp.Server).AddTool panics on. Each one
// must be skipped, the valid tools of the same list must still be registered,
// and nothing may panic (a panic here runs outside middleware.Recover and
// would take the whole gateway process down).
func TestRegisterSessionTools_InvalidDefinition_SkippedWithoutPanic(t *testing.T) {
	const okTool = `{"name":"ok","inputSchema":{"type":"object"}}`
	cases := []struct {
		name string
		evil string
	}{
		{"missing inputSchema", `{"name":"evil"}`},
		{"non-object inputSchema", `{"name":"evil","inputSchema":{"type":"string"}}`},
		{"inputSchema is not an object", `{"name":"evil","inputSchema":"object"}`},
		{"null inputSchema", `{"name":"evil","inputSchema":null}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var list mcp.ListToolsResult
			require.NoError(t, json.Unmarshal([]byte(`{"tools":[`+tc.evil+`,`+okTool+`]}`), &list))

			front := mcp.NewServer(&mcp.Implementation{Name: "front", Version: "0.0.1"}, nil)
			require.NotPanics(t, func() {
				RegisterSessionTools(t.Context(), front, list.Tools,
					func(context.Context) (*mcp.ClientSession, error) {
						return nil, errors.New("unused")
					})
			})

			clientTransport, serverTransport := mcp.NewInMemoryTransports()
			_, err := front.Connect(t.Context(), serverTransport, nil)
			require.NoError(t, err)
			client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
			session, err := client.Connect(t.Context(), clientTransport, nil)
			require.NoError(t, err)
			defer session.Close()

			result, err := session.ListTools(t.Context(), nil)
			require.NoError(t, err)
			names := make([]string, 0, len(result.Tools))
			for _, tool := range result.Tools {
				names = append(names, tool.Name)
			}
			require.Equal(t, []string{"ok"}, names)
		})
	}
}

func TestValidateSessionTool(t *testing.T) {
	require.Error(t, validateSessionTool(nil))
	require.Error(t, validateSessionTool(&mcp.Tool{Name: "x"}))
	require.Error(t, validateSessionTool(&mcp.Tool{
		Name:        "x",
		InputSchema: map[string]any{"type": "array"},
	}))
	require.Error(t, validateSessionTool(&mcp.Tool{Name: "x", InputSchema: []any{"object"}}))
	require.Error(t, validateSessionTool(&mcp.Tool{
		Name:         "x",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: make(chan int),
	}))
	require.NoError(t, validateSessionTool(&mcp.Tool{
		Name:        "x",
		InputSchema: map[string]any{"type": "object"},
	}))
	require.NoError(t, validateSessionTool(&mcp.Tool{
		Name:         "x",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{}}`),
		OutputSchema: map[string]any{"type": "object"},
	}))
}
