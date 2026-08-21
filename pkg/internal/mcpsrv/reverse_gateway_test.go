package mcpsrv

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/stretchr/testify/require"
)

func newTestReverseGateway(
	t *testing.T,
	servers config.Servers,
	edgeCfg config.EdgeConfig,
) *ReverseGateway {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	registry := edgeservices.NewInMemoryRegistry()
	gateway := NewReverseGateway(registry, pairing, edgeCfg.WithDefaults(), servers)
	gateway.Init(t.Context())
	return gateway
}

func staticReverseServers() config.Servers {
	return config.Servers{
		"app1": {
			Name:        "app1",
			Description: "app1",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
	}
}

func staticEdgeConfig() config.EdgeConfig {
	return config.EdgeConfig{
		Auth:    config.EdgeAuthPairing,
		Pairing: config.PairingConfig{Type: config.PairingTypeStatic},
	}
}

// bridgedFrameSender forwards a written frame's payload into the peer side's
// incoming channel, simulating a single physical edge WebSocket connection
// carrying one binding's traffic.
type bridgedFrameSender struct {
	peerIncoming chan json.RawMessage
}

func (s *bridgedFrameSender) SendEdgeFrame(_ context.Context, frame EdgeEnvelope) error {
	s.peerIncoming <- frame.Payload
	return nil
}

// connectFakeTab wires a fake WebMCP page's mcp.Server to gateway via
// HandleAppUp, as if a browser tab had just connected. It returns the page's
// mcp.Server so the test can mutate its tools further (e.g. list_changed).
func connectFakeTab(
	t *testing.T,
	gateway *ReverseGateway,
	binding domainedge.Binding,
) *mcp.Server {
	t.Helper()
	toGateway := make(chan json.RawMessage, 16)
	toPage := make(chan json.RawMessage, 16)

	page := mcp.NewServer(&mcp.Implementation{Name: "page", Version: "0.0.1"}, nil)
	page.AddTool(
		&mcp.Tool{
			Name:        "read_dom",
			Description: "read the page DOM",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "dom-snapshot"}},
			}, nil
		},
	)
	pageTransport := &EdgeTransport{
		Origin:     binding.Origin,
		AppSession: binding.AppSession,
		Sender:     &bridgedFrameSender{peerIncoming: toGateway},
		Incoming:   toPage,
	}
	_, err := page.Connect(t.Context(), pageTransport, nil)
	require.NoError(t, err)

	sender := &bridgedFrameSender{peerIncoming: toPage}
	require.NoError(t, gateway.HandleAppUp(t.Context(), binding, sender, toGateway))
	return page
}

func TestReverseGateway_HandleAppUp_UnknownOrigin_Error(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	binding := domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://unknown.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	err := gateway.HandleAppUp(
		t.Context(),
		binding,
		&bridgedFrameSender{},
		make(chan json.RawMessage),
	)
	require.Error(t, err)
}

func TestReverseGateway_HasServer(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	require.True(t, gateway.HasServer("app1"))
	require.False(t, gateway.HasServer("does-not-exist"))
}

func TestReverseGateway_ResolveServer_UnknownName_Error(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	_, err := gateway.ResolveServer(t.Context(), "does-not-exist")
	require.Error(t, err)
}

func TestReverseGateway_ResolveServer_BeforeAnyAppUp_HasPairingTool(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	srv, err := gateway.ResolveServer(t.Context(), "app1")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestReverseGateway_CreatePairingCodeTool_IssuesCode(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	srv, err := gateway.ResolveServer(t.Context(), "app1")
	require.NoError(t, err)

	callerTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), callerTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: createPairingCodeToolName},
	)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
}

func TestReverseGateway_HandleAppUp_ThenResolveServer_ForwardsToolCall(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	binding := domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	connectFakeTab(t, gateway, binding)

	srv, err := gateway.ResolveServer(t.Context(), "app1")
	require.NoError(t, err)

	callerTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), callerTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_dom"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "dom-snapshot", text.Text)
}

func TestReverseGateway_HandleAppDown_ToolCallReturnsFriendlyError(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	binding := domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	connectFakeTab(t, gateway, binding)
	gateway.HandleAppDown(t.Context(), binding.IdentityKey, binding.Origin, binding.AppSession)

	srv, err := gateway.ResolveServer(t.Context(), "app1")
	require.NoError(t, err)

	callerTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), callerTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_dom"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	text, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Contains(t, text.Text, binding.Origin)
}

func TestReverseGateway_DropConnection_DropsAllBoundOrigins(t *testing.T) {
	servers := config.Servers{
		"app1": {
			Name:        "app1",
			Description: "app1",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
		"app2": {
			Name:        "app2",
			Description: "app2",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app2.example.com",
		},
	}
	gateway := newTestReverseGateway(t, servers, staticEdgeConfig())
	binding1 := domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	binding2 := domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://app2.example.com",
		AppSession:  "session-2",
		ConnID:      "conn-1",
	}
	connectFakeTab(t, gateway, binding1)
	connectFakeTab(t, gateway, binding2)

	gateway.DropConnection(t.Context(), "conn-1")

	for _, name := range []string{"app1", "app2"} {
		srv, err := gateway.ResolveServer(t.Context(), name)
		require.NoError(t, err)
		callerTransport, serverTransport := mcp.NewInMemoryTransports()
		_, err = srv.Connect(t.Context(), serverTransport, nil)
		require.NoError(t, err)
		client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
		session, err := client.Connect(t.Context(), callerTransport, nil)
		require.NoError(t, err)
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_dom"})
		require.NoError(t, err)
		require.True(t, result.IsError, "server %s should report tab disconnected", name)
		session.Close()
	}
}

func TestIdentityKeyForRequest_Static_ReturnsStaticKey(t *testing.T) {
	key, err := IdentityKeyForRequest(staticEdgeConfig().WithDefaults())
	require.NoError(t, err)
	require.Equal(t, domainedge.StaticIdentityKey, key)
}

func TestIdentityKeyForRequest_Remote_NotImplemented(t *testing.T) {
	_, err := IdentityKeyForRequest(config.EdgeConfig{}.WithDefaults())
	require.Error(t, err)
}
