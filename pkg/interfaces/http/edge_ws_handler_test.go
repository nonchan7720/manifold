package httphandler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/stretchr/testify/require"
)

func staticReverseServersForWS() config.Servers {
	return config.Servers{
		"app1": {
			Name:        "app1",
			Description: "app1",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
	}
}

func newTestEdgeWSHandler(
	t *testing.T,
	edgeCfg config.EdgeConfig,
) (*EdgeWSHandler, *edgeservices.PairingService) {
	handler, pairing, _, _ := newTestEdgeWSHandlerWithDeps(t, edgeCfg)
	return handler, pairing
}

// newTestEdgeWSHandlerWithDeps also returns the registry and store backing
// handler, so tests can assert on bindings for several identityKeys at once
// (registry.Resolve) or seed store state the public pairing API cannot
// produce (e.g. a zero-binding token).
func newTestEdgeWSHandlerWithDeps(
	t *testing.T,
	edgeCfg config.EdgeConfig,
) (*EdgeWSHandler, *edgeservices.PairingService, *edgeservices.InMemoryRegistry, *memory.Client) {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	registry := edgeservices.NewInMemoryRegistry()
	gateway := mcpsrv.NewReverseGateway(
		registry,
		pairing,
		edgeCfg.WithDefaults(),
		staticReverseServersForWS(),
	)
	gateway.Init(t.Context())
	handler := NewEdgeWSHandler(edgeCfg.WithDefaults(), pairing, gateway)
	return handler, pairing, registry, storeClient
}

// issueMultiKeyEdgeToken pairs a single edge token to every key in keys, as
// if the extension had paired once per profile-scoped server sharing that
// token (see "ペアリングのプロファイル対応" in
// docs/design/webmcp-reverse-gateway-phase2.ja.md).
func issueMultiKeyEdgeToken(
	t *testing.T,
	pairing *edgeservices.PairingService,
	keys ...domainedge.IdentityKey,
) string {
	t.Helper()
	var token string
	for _, key := range keys {
		code, err := pairing.IssueCode(t.Context(), key)
		require.NoError(t, err)
		token, err = pairing.ExchangeCode(t.Context(), code, token)
		require.NoError(t, err)
	}
	return token
}

func wsURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func issueEdgeToken(t *testing.T, pairing *edgeservices.PairingService) string {
	t.Helper()
	code, err := pairing.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)
	token, err := pairing.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)
	return token
}

func TestEdgeWSHandler_ValidAuth_ReceivesReadyFrame(t *testing.T) {
	handler, pairing := newTestEdgeWSHandler(t, staticEdgeConfigForWS())
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// coder/websocket のドキュメント通り、成功時は resp.Body が nil になるため
	// 呼び出し側で Close する必要はない（呼ぶと nil pointer になる）。
	conn, _, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.NoError(t, err)
	defer conn.CloseNow()

	token := issueEdgeToken(t, pairing)
	require.NoError(
		t,
		wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFrameAuth, Token: token}),
	)

	var ready mcpsrv.EdgeEnvelope
	require.NoError(t, wsjson.Read(ctx, conn, &ready))
	require.Equal(t, mcpsrv.EdgeFrameReady, ready.Type)
	require.Equal(t, 20, ready.HeartbeatSec)
	require.Contains(t, ready.Origins, "https://app1.example.com")
}

func TestEdgeWSHandler_InvalidToken_Closed4401(t *testing.T) {
	handler, _ := newTestEdgeWSHandler(t, staticEdgeConfigForWS())
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// coder/websocket のドキュメント通り、成功時は resp.Body が nil になるため
	// 呼び出し側で Close する必要はない（呼ぶと nil pointer になる）。
	conn, _, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.NoError(t, err)
	defer conn.CloseNow()

	require.NoError(t, wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{
		Type:  mcpsrv.EdgeFrameAuth,
		Token: "bogus-token",
	}))

	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	require.Contains(t, websocket.CloseStatus(err).String(), "4401")
}

func TestEdgeWSHandler_FirstFrameNotAuth_Closed4401(t *testing.T) {
	handler, _ := newTestEdgeWSHandler(t, staticEdgeConfigForWS())
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// coder/websocket のドキュメント通り、成功時は resp.Body が nil になるため
	// 呼び出し側で Close する必要はない（呼ぶと nil pointer になる）。
	conn, _, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.NoError(t, err)
	defer conn.CloseNow()

	require.NoError(t, wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFramePing}))

	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	require.EqualValues(t, 4401, websocket.CloseStatus(err))
}

func TestEdgeWSHandler_Ping_RepliesPong(t *testing.T) {
	handler, pairing := newTestEdgeWSHandler(t, staticEdgeConfigForWS())
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// coder/websocket のドキュメント通り、成功時は resp.Body が nil になるため
	// 呼び出し側で Close する必要はない（呼ぶと nil pointer になる）。
	conn, _, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.NoError(t, err)
	defer conn.CloseNow()

	token := issueEdgeToken(t, pairing)
	require.NoError(
		t,
		wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFrameAuth, Token: token}),
	)
	var ready mcpsrv.EdgeEnvelope
	require.NoError(t, wsjson.Read(ctx, conn, &ready))

	require.NoError(t, wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFramePing}))
	var pong mcpsrv.EdgeEnvelope
	require.NoError(t, wsjson.Read(ctx, conn, &pong))
	require.Equal(t, mcpsrv.EdgeFramePong, pong.Type)
}

func TestEdgeWSHandler_AppUp_UnknownOrigin_ReceivesErrorFrame(t *testing.T) {
	handler, pairing := newTestEdgeWSHandler(t, staticEdgeConfigForWS())
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// coder/websocket のドキュメント通り、成功時は resp.Body が nil になるため
	// 呼び出し側で Close する必要はない（呼ぶと nil pointer になる）。
	conn, _, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.NoError(t, err)
	defer conn.CloseNow()

	token := issueEdgeToken(t, pairing)
	require.NoError(
		t,
		wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFrameAuth, Token: token}),
	)
	var ready mcpsrv.EdgeEnvelope
	require.NoError(t, wsjson.Read(ctx, conn, &ready))

	require.NoError(t, wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{
		Type:       mcpsrv.EdgeFrameAppUp,
		Origin:     "https://unknown.example.com",
		AppSession: "session-1",
	}))

	var errFrame mcpsrv.EdgeEnvelope
	require.NoError(t, wsjson.Read(ctx, conn, &errFrame))
	require.Equal(t, mcpsrv.EdgeFrameError, errFrame.Type)

	// 接続は維持され、以降のフレームにも応答できる
	require.NoError(t, wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{Type: mcpsrv.EdgeFramePing}))
	var pong mcpsrv.EdgeEnvelope
	require.NoError(t, wsjson.Read(ctx, conn, &pong))
	require.Equal(t, mcpsrv.EdgeFramePong, pong.Type)
}

func TestEdgeWSHandler_ForwardAuth_NotImplemented(t *testing.T) {
	edgeCfg := config.EdgeConfig{Auth: config.EdgeAuthForwardAuth}.WithDefaults()
	handler, _ := newTestEdgeWSHandler(t, edgeCfg)
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, 501, resp.StatusCode)
}

// pushFrameSender implements mcpsrv.EdgeFrameSender by forwarding an "mcp"
// frame's payload into a channel, bridging a fake WebMCP page's transport to
// an edgeConnSession under test without a real WebSocket connection.
type pushFrameSender struct {
	to chan json.RawMessage
}

func (s *pushFrameSender) SendEdgeFrame(_ context.Context, frame mcpsrv.EdgeEnvelope) error {
	if frame.Type == mcpsrv.EdgeFrameMCP {
		s.to <- frame.Payload
	}
	return nil
}

// connectFakeTabToSession simulates a browser tab's WebMCP page connecting
// through sess.handleAppUp, without an actual WebSocket connection. It
// mirrors mcpsrv's connectFakeTab, adapted to go through edgeConnSession so
// the session's full identityKeys set (not just a single binding) drives the
// bind.
func connectFakeTabToSession(t *testing.T, sess *edgeConnSession, origin, appSession string) {
	t.Helper()
	toPage := make(chan json.RawMessage, 16)
	sess.sender = &pushFrameSender{to: toPage}

	sess.handleAppUp(t.Context(), mcpsrv.EdgeEnvelope{
		Type:       mcpsrv.EdgeFrameAppUp,
		Origin:     origin,
		AppSession: appSession,
	})

	sess.mu.Lock()
	toGateway := sess.channels[bindingChanKey(origin, appSession)]
	sess.mu.Unlock()
	require.NotNil(t, toGateway, "handleAppUp must register the binding channel synchronously")

	page := mcp.NewServer(&mcp.Implementation{Name: "page", Version: "0.0.1"}, nil)
	pageTransport := &mcpsrv.EdgeTransport{
		Origin:     origin,
		AppSession: appSession,
		Sender:     &pushFrameSender{to: toGateway},
		Incoming:   toPage,
	}
	_, err := page.Connect(t.Context(), pageTransport, nil)
	require.NoError(t, err)
}

func TestEdgeWSHandler_AppUp_MultipleIdentityKeys_BindsEachKey(t *testing.T) {
	handler, pairing, registry, _ := newTestEdgeWSHandlerWithDeps(t, staticEdgeConfigForWS())
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}
	token := issueMultiKeyEdgeToken(t, pairing, identityKeys...)
	authedKeys, err := pairing.Authenticate(t.Context(), token)
	require.NoError(t, err)

	const origin, appSession = "https://app1.example.com", "session-1"
	sess := &edgeConnSession{
		connID:       "conn-1",
		identityKeys: authedKeys,
		gateway:      handler.gateway,
		channels:     map[string]chan json.RawMessage{},
	}
	connectFakeTabToSession(t, sess, origin, appSession)

	require.Eventually(t, func() bool {
		for _, key := range identityKeys {
			if _, ok := registry.Resolve(t.Context(), key, origin); !ok {
				return false
			}
		}
		return true
	}, 2*time.Second, 10*time.Millisecond, "app.up should bind every identityKey on the token")
}

func TestEdgeWSHandler_AppDown_MultipleIdentityKeys_UnbindsAllKeys(t *testing.T) {
	handler, pairing, registry, _ := newTestEdgeWSHandlerWithDeps(t, staticEdgeConfigForWS())
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}
	token := issueMultiKeyEdgeToken(t, pairing, identityKeys...)
	authedKeys, err := pairing.Authenticate(t.Context(), token)
	require.NoError(t, err)

	const origin, appSession = "https://app1.example.com", "session-1"
	sess := &edgeConnSession{
		connID:       "conn-1",
		identityKeys: authedKeys,
		gateway:      handler.gateway,
		channels:     map[string]chan json.RawMessage{},
	}
	connectFakeTabToSession(t, sess, origin, appSession)
	require.Eventually(t, func() bool {
		for _, key := range identityKeys {
			if _, ok := registry.Resolve(t.Context(), key, origin); !ok {
				return false
			}
		}
		return true
	}, 2*time.Second, 10*time.Millisecond, "setup: app.up should bind every identityKey")

	sess.handleAppDown(t.Context(), mcpsrv.EdgeEnvelope{Origin: origin, AppSession: appSession})

	for _, key := range identityKeys {
		_, ok := registry.Resolve(t.Context(), key, origin)
		require.False(t, ok, "identityKey %s should be unbound after app.down", key)
	}
}

func TestEdgeWSHandler_Disconnect_MultipleIdentityKeys_UnbindsAllKeys(t *testing.T) {
	handler, pairing, registry, _ := newTestEdgeWSHandlerWithDeps(t, staticEdgeConfigForWS())
	identityKeys := []domainedge.IdentityKey{"oauth:user-a", "saml:user-a"}
	token := issueMultiKeyEdgeToken(t, pairing, identityKeys...)
	authedKeys, err := pairing.Authenticate(t.Context(), token)
	require.NoError(t, err)

	const origin, appSession = "https://app1.example.com", "session-1"
	sess := &edgeConnSession{
		connID:       "conn-1",
		identityKeys: authedKeys,
		gateway:      handler.gateway,
		channels:     map[string]chan json.RawMessage{},
	}
	connectFakeTabToSession(t, sess, origin, appSession)
	require.Eventually(t, func() bool {
		for _, key := range identityKeys {
			if _, ok := registry.Resolve(t.Context(), key, origin); !ok {
				return false
			}
		}
		return true
	}, 2*time.Second, 10*time.Millisecond, "setup: app.up should bind every identityKey")

	handler.gateway.DropConnection(t.Context(), sess.connID)

	for _, key := range identityKeys {
		_, ok := registry.Resolve(t.Context(), key, origin)
		require.False(t, ok, "identityKey %s should be dropped on disconnect", key)
	}
}

func TestEdgeWSHandler_ZeroBindingToken_Closed4401(t *testing.T) {
	handler, _, _, storeClient := newTestEdgeWSHandlerWithDeps(t, staticEdgeConfigForWS())
	server := httptest.NewServer(handler)
	defer server.Close()

	// A zero-binding token cannot arise from the public pairing API (every
	// ExchangeCode call adds at least one identityKey); seed it directly to
	// exercise the defensive rejection path.
	require.NoError(t, storeClient.Set(
		t.Context(),
		"edge:token:zero-binding",
		[]byte(`{"identityKeys":[],"revoked":false}`),
		time.Hour,
	))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	// coder/websocket のドキュメント通り、成功時は resp.Body が nil になるため
	// 呼び出し側で Close する必要はない（呼ぶと nil pointer になる）。
	conn, _, err := websocket.Dial(ctx, wsURL(server), nil) //nolint:bodyclose
	require.NoError(t, err)
	defer conn.CloseNow()

	require.NoError(t, wsjson.Write(ctx, conn, mcpsrv.EdgeEnvelope{
		Type:  mcpsrv.EdgeFrameAuth,
		Token: "zero-binding",
	}))

	_, _, err = conn.Read(ctx)
	require.Error(t, err)
	require.EqualValues(t, 4401, websocket.CloseStatus(err))
}

func TestEdgeConnSession_CloseAllChannels_ClosesAndClearsEveryEntry(t *testing.T) {
	sess := &edgeConnSession{
		channels: map[string]chan json.RawMessage{
			"a": make(chan json.RawMessage, 1),
			"b": make(chan json.RawMessage, 1),
		},
	}
	sess.closeAllChannels()
	require.Empty(t, sess.channels)
}

func TestEdgeConnSession_RouteMCPFrame_DropsWhenChannelFull(t *testing.T) {
	// Sending must never block the read loop that drives every dispatch; a
	// full buffer drops the frame instead.
	sess := &edgeConnSession{channels: map[string]chan json.RawMessage{}}
	key := bindingChanKey("https://app1.example.com", "session-1")
	ch := make(chan json.RawMessage, 1)
	ch <- json.RawMessage(`{"already":"buffered"}`)
	sess.channels[key] = ch

	require.NotPanics(t, func() {
		sess.routeMCPFrame(mcpsrv.EdgeEnvelope{
			Origin:     "https://app1.example.com",
			AppSession: "session-1",
			Payload:    json.RawMessage(`{"dropped":true}`),
		})
	})
	require.Len(t, ch, 1, "the full buffer's existing message should be left in place")
}

func staticEdgeConfigForWS() config.EdgeConfig {
	return config.EdgeConfig{
		Auth:    config.EdgeAuthPairing,
		Pairing: config.PairingConfig{Type: config.PairingTypeStatic},
	}
}
