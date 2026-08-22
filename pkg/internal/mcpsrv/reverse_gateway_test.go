package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	gateway, _ := newTestReverseGatewayWithRegistry(t, servers, edgeCfg)
	return gateway
}

// newTestReverseGatewayWithRegistry also returns the registry backing
// gateway, so tests can assert on bindings for identityKeys that the
// gateway's own edgeCfg-derived routing (ResolveServer/IdentityKeyForRequest)
// cannot reach (e.g. non-static identityKeys under a static-config gateway).
func newTestReverseGatewayWithRegistry(
	t *testing.T,
	servers config.Servers,
	edgeCfg config.EdgeConfig,
) (*ReverseGateway, *edgeservices.InMemoryRegistry) {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	registry := edgeservices.NewInMemoryRegistry()
	gateway := NewReverseGateway(registry, pairing, edgeCfg.WithDefaults(), servers)
	gateway.Init(t.Context())
	return gateway, registry
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

// staticResolveCtx returns the context ResolveServer expects for a
// pairing/static deployment: mcpAuthMiddleware always sets
// domainedge.StaticIdentityKey there (see reverse_gateway.go).
func staticResolveCtx(t *testing.T) context.Context {
	t.Helper()
	return domainedge.WithIdentityKey(t.Context(), domainedge.StaticIdentityKey)
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
	identityKeys := []domainedge.IdentityKey{binding.IdentityKey}
	return connectFakeTabMultiKey(t, gateway, identityKeys, binding)
}

// connectFakeTabMultiKey is connectFakeTab for a binding shared by several
// identityKeys (e.g. an edge token paired to more than one profile).
func connectFakeTabMultiKey(
	t *testing.T,
	gateway *ReverseGateway,
	identityKeys []domainedge.IdentityKey,
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
	require.NoError(t, gateway.HandleAppUp(
		t.Context(),
		identityKeys,
		binding.Origin,
		binding.AppSession,
		binding.ConnID,
		sender,
		toGateway,
	))
	return page
}

func TestReverseGateway_HandleAppUp_UnknownOrigin_Error(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	err := gateway.HandleAppUp(
		t.Context(),
		[]domainedge.IdentityKey{domainedge.StaticIdentityKey},
		"https://unknown.example.com",
		"session-1",
		"conn-1",
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
	srv, err := gateway.ResolveServer(staticResolveCtx(t), "app1")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestReverseGateway_CreatePairingCodeTool_IssuesCode(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	srv, err := gateway.ResolveServer(staticResolveCtx(t), "app1")
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

	srv, err := gateway.ResolveServer(staticResolveCtx(t), "app1")
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
	gateway.HandleAppDown(
		t.Context(),
		[]domainedge.IdentityKey{binding.IdentityKey},
		binding.Origin,
		binding.AppSession,
	)

	srv, err := gateway.ResolveServer(staticResolveCtx(t), "app1")
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
		srv, err := gateway.ResolveServer(staticResolveCtx(t), name)
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

func TestReverseGateway_HandleAppUp_ToolListChanged_ExposesNewTool(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	binding := domainedge.Binding{
		IdentityKey: domainedge.StaticIdentityKey,
		Origin:      "https://app1.example.com",
		AppSession:  "session-1",
		ConnID:      "conn-1",
	}
	page := connectFakeTab(t, gateway, binding)

	// Adding a tool after HandleAppUp has returned exercises
	// ToolListChangedHandler with the session already assigned; it must not
	// use a stale/nil session captured before client.Connect returned.
	page.AddTool(
		&mcp.Tool{
			Name:        "new_tool",
			Description: "added after connect",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "new-tool-result"}},
			}, nil
		},
	)

	require.Eventually(t, func() bool {
		srv, err := gateway.ResolveServer(staticResolveCtx(t), "app1")
		if err != nil {
			return false
		}
		callerTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := srv.Connect(t.Context(), serverTransport, nil); err != nil {
			return false
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
		session, err := client.Connect(t.Context(), callerTransport, nil)
		if err != nil {
			return false
		}
		defer session.Close()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "new_tool"})
		return err == nil && !result.IsError
	}, 2*time.Second, 10*time.Millisecond, "new_tool should become callable once list_changed rebuilds the server")
}

func TestReverseGateway_HandleAppUp_MultipleIdentityKeys_BindsEachKey(t *testing.T) {
	gateway, registry := newTestReverseGatewayWithRegistry(
		t,
		staticReverseServers(),
		staticEdgeConfig(),
	)
	binding := domainedge.Binding{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		ConnID:     "conn-1",
	}
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}
	connectFakeTabMultiKey(t, gateway, identityKeys, binding)

	for _, key := range identityKeys {
		handle, ok := registry.Resolve(t.Context(), key, binding.Origin)
		require.True(t, ok, "identityKey %s should be bound", key)
		session, ok := handle.(*mcp.ClientSession)
		require.True(t, ok)
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_dom"})
		require.NoError(t, err)
		require.False(t, result.IsError)
	}
}

func TestReverseGateway_HandleAppDown_MultipleIdentityKeys_UnbindsAllKeys(t *testing.T) {
	gateway, registry := newTestReverseGatewayWithRegistry(
		t,
		staticReverseServers(),
		staticEdgeConfig(),
	)
	binding := domainedge.Binding{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		ConnID:     "conn-1",
	}
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}
	connectFakeTabMultiKey(t, gateway, identityKeys, binding)

	gateway.HandleAppDown(t.Context(), identityKeys, binding.Origin, binding.AppSession)

	for _, key := range identityKeys {
		_, ok := registry.Resolve(t.Context(), key, binding.Origin)
		require.False(t, ok, "identityKey %s should be unbound", key)
	}
}

func TestReverseGateway_DropConnection_MultipleIdentityKeys_DropsAllKeys(t *testing.T) {
	gateway, registry := newTestReverseGatewayWithRegistry(
		t,
		staticReverseServers(),
		staticEdgeConfig(),
	)
	binding := domainedge.Binding{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		ConnID:     "conn-1",
	}
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}
	connectFakeTabMultiKey(t, gateway, identityKeys, binding)

	gateway.DropConnection(t.Context(), binding.ConnID)

	for _, key := range identityKeys {
		_, ok := registry.Resolve(t.Context(), key, binding.Origin)
		require.False(t, ok, "identityKey %s should be dropped", key)
	}
}

func TestReverseGateway_HandleAppUp_RebuildFailure_RollsBackAllBindings(t *testing.T) {
	gateway, registry := newTestReverseGatewayWithRegistry(
		t,
		staticReverseServers(),
		staticEdgeConfig(),
	)
	binding := domainedge.Binding{
		Origin:     "https://app1.example.com",
		AppSession: "session-1",
		ConnID:     "conn-1",
	}
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}

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
	// Fail the second identityKey's tools/list call (its rebuildUserServer),
	// so a correct HandleAppUp must not have bound the first identityKey yet
	// when it rolls back.
	var listCalls atomic.Int32
	page.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" && listCalls.Add(1) == 2 {
				return nil, errors.New("boom")
			}
			return next(ctx, method, req)
		}
	})
	pageTransport := &EdgeTransport{
		Origin:     binding.Origin,
		AppSession: binding.AppSession,
		Sender:     &bridgedFrameSender{peerIncoming: toGateway},
		Incoming:   toPage,
	}
	_, err := page.Connect(t.Context(), pageTransport, nil)
	require.NoError(t, err)

	sender := &bridgedFrameSender{peerIncoming: toPage}
	err = gateway.HandleAppUp(
		t.Context(),
		identityKeys,
		binding.Origin,
		binding.AppSession,
		binding.ConnID,
		sender,
		toGateway,
	)
	require.Error(t, err)

	for _, key := range identityKeys {
		_, ok := registry.Resolve(t.Context(), key, binding.Origin)
		require.False(t, ok, "identityKey %s must not be bound after rollback", key)
	}
}

func TestCloseUniqueHandles_ClosesEachDistinctHandleOnce(t *testing.T) {
	var closedCount int
	closeUniqueHandles([]any{"session-a", "session-a", "session-b"}, func(any) {
		closedCount++
	})
	require.Equal(t, 2, closedCount)
}

// --- ResolveServer: identityKey comes from context, not edgeCfg ---

func TestReverseGateway_ResolveServer_NoIdentityKeyInContext_Error(t *testing.T) {
	gateway := newTestReverseGateway(t, staticReverseServers(), staticEdgeConfig())
	_, err := gateway.ResolveServer(t.Context(), "app1")
	require.Error(t, err)
}

func TestReverseGateway_ResolveServer_RemotePairing_LazilyBuildsBaseServer(t *testing.T) {
	// remote には Init 時点で分かる identityKey が無いため、Init は何も事前構築しない。
	// ResolveServer が初回アクセス時に create_pairing_code だけの基底サーバーを
	// 遅延構築し、未ペアリング → create_pairing_code の導線を成立させる。
	gateway := newTestReverseGateway(
		t,
		staticReverseServers(),
		config.EdgeConfig{Pairing: config.PairingConfig{Type: config.PairingTypeRemote}},
	)
	ctx := domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("oauth:user-a"))

	srv, err := gateway.ResolveServer(ctx, "app1")
	require.NoError(t, err)
	require.NotNil(t, srv)

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
	require.False(t, result.IsError, "a lazily-built server must still expose create_pairing_code")
}

func TestReverseGateway_ResolveServer_RemotePairing_DifferentIdentityKeys_DistinctServers(
	t *testing.T,
) {
	gateway := newTestReverseGateway(
		t,
		staticReverseServers(),
		config.EdgeConfig{Pairing: config.PairingConfig{Type: config.PairingTypeRemote}},
	)
	ctxA := domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	ctxB := domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("oauth:user-b"))

	srvA, err := gateway.ResolveServer(ctxA, "app1")
	require.NoError(t, err)
	srvB, err := gateway.ResolveServer(ctxB, "app1")
	require.NoError(t, err)

	require.NotSame(t, srvA, srvB, "each identityKey must get its own per-user server")
}

func TestReverseGateway_ResolveServer_ConcurrentFirstAccess_BuildsOnce(t *testing.T) {
	gateway := newTestReverseGateway(
		t,
		staticReverseServers(),
		config.EdgeConfig{Pairing: config.PairingConfig{Type: config.PairingTypeRemote}},
	)
	ctx := domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("oauth:user-concurrent"))

	const n = 20
	servers := make([]*mcp.Server, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			servers[i], errs[i] = gateway.ResolveServer(ctx, "app1")
		}(i)
	}
	wg.Wait()

	for i := range n {
		require.NoError(t, errs[i])
		require.Same(
			t,
			servers[0],
			servers[i],
			"concurrent first access must not build more than one server",
		)
	}
}

// TestReverseGateway_ConcurrentResolveServerAndHandleAppUp_SessionToolsSurvive guards
// against ResolveServer's lazy first-build racing HandleAppUp's rebuild for
// the same brand-new identityKey: both write via rebuildUserServer under
// g.mu, so if ResolveServer's base (session=nil) build wins the write after
// HandleAppUp's session-carrying build already landed, the session's tools
// would silently disappear behind create_pairing_code-only again.
func TestReverseGateway_ConcurrentResolveServerAndHandleAppUp_SessionToolsSurvive(t *testing.T) {
	gateway := newTestReverseGateway(
		t,
		staticReverseServers(),
		config.EdgeConfig{Pairing: config.PairingConfig{Type: config.PairingTypeRemote}},
	)

	const rounds = 50
	for i := range rounds {
		identityKey := domainedge.IdentityKey(fmt.Sprintf("oauth:user-%d", i))
		ctx := domainedge.WithIdentityKey(t.Context(), identityKey)
		binding := domainedge.Binding{
			IdentityKey: identityKey,
			Origin:      "https://app1.example.com",
			AppSession:  fmt.Sprintf("session-%d", i),
			ConnID:      fmt.Sprintf("conn-%d", i),
		}

		toGateway := make(chan json.RawMessage, 16)
		toPage := make(chan json.RawMessage, 16)
		page := mcp.NewServer(&mcp.Implementation{Name: "page", Version: "0.0.1"}, nil)
		page.AddTool(
			&mcp.Tool{Name: "read_dom", InputSchema: map[string]any{"type": "object"}},
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

		var start sync.WaitGroup
		start.Add(1)
		var wg sync.WaitGroup
		var handleAppUpErr error
		wg.Go(func() {
			start.Wait()
			_, _ = gateway.ResolveServer(ctx, "app1")
		})
		wg.Go(func() {
			start.Wait()
			handleAppUpErr = gateway.HandleAppUp(
				t.Context(),
				[]domainedge.IdentityKey{identityKey},
				binding.Origin, binding.AppSession, binding.ConnID,
				&bridgedFrameSender{peerIncoming: toPage}, toGateway,
			)
		})
		start.Done()
		wg.Wait()
		require.NoError(t, handleAppUpErr, "round %d", i)

		srv, err := gateway.ResolveServer(ctx, "app1")
		require.NoError(t, err)
		callerTransport, serverTransport := mcp.NewInMemoryTransports()
		_, err = srv.Connect(t.Context(), serverTransport, nil)
		require.NoError(t, err)
		client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
		session, err := client.Connect(t.Context(), callerTransport, nil)
		require.NoError(t, err)
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "read_dom"})
		session.Close()
		require.NoError(t, err)
		require.False(t, result.IsError,
			"round %d: HandleAppUp's session tools must survive a concurrent "+
				"ResolveServer lazy-build", i)
	}
}
