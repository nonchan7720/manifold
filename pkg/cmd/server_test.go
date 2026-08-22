package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/coder/websocket"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
	"github.com/nonchan7720/manifold/pkg/services/identity"
	"github.com/stretchr/testify/require"
)

// withGlobalConfig は globalConfig を一時的に差し替え、テスト終了時に元に戻す。
func withGlobalConfig(t *testing.T, cfg *config.Config) {
	t.Helper()
	prev := globalConfig
	globalConfig = cfg
	t.Cleanup(func() { globalConfig = prev })
}

func TestNewStoreClient_SQLite(t *testing.T) {
	withGlobalConfig(t, &config.Config{
		SQLite: &config.SQLiteConfig{Path: ":memory:"},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &sqlite.Client{}, c)
}

func TestNewStoreClient_Memory(t *testing.T) {
	withGlobalConfig(t, &config.Config{
		Memory: &config.MemoryConfig{Enabled: true},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &memory.Client{}, c)
}

func TestNewStoreClient_SQLiteNil_DoesNotPanic(t *testing.T) {
	// SQLite が nil の場合でもパニックせず、Memory 分岐にフォールバックできること
	withGlobalConfig(t, &config.Config{
		SQLite: nil,
		Memory: &config.MemoryConfig{Enabled: true},
	})

	require.NotPanics(t, func() {
		c, err := newStoreClient(t.Context())
		require.NoError(t, err)
		defer c.Close()
	})
}

func TestNewStoreClient_MemoryDisabled_FallsBackToMemory(t *testing.T) {
	// memory セクションだけが存在し enabled が false でも、Redis 設定が無ければ
	// パニックせずインメモリにフォールバックすること
	withGlobalConfig(t, &config.Config{
		Memory: &config.MemoryConfig{Enabled: false},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &memory.Client{}, c)
}

func TestNewStoreClient_SQLiteEmptyPath_FallsBackToMemory(t *testing.T) {
	// sqlite.path が空文字の場合も Redis 設定が無ければインメモリにフォールバックすること
	withGlobalConfig(t, &config.Config{
		SQLite: &config.SQLiteConfig{Path: ""},
	})

	c, err := newStoreClient(t.Context())
	require.NoError(t, err)
	defer c.Close()

	require.IsType(t, &memory.Client{}, c)
}

func TestNewStoreClient_MemoryDisabled_PrefersRedis(t *testing.T) {
	// memory.enabled が false の場合は Redis 設定が優先されること。
	// 接続先が存在しないため接続エラーになるが、インメモリにフォールバックしないことを確認する。
	withGlobalConfig(t, &config.Config{
		Memory: &config.MemoryConfig{Enabled: false},
		Redis:  &config.RedisConfig{Addrs: []string{"127.0.0.1:1"}},
	})

	c, err := newStoreClient(t.Context())
	require.Error(t, err, "redis へ接続を試みるべき")
	require.ErrorContains(t, err, "redis")
	require.Nil(t, c)
}

// --- resolveMCPServer ---

func newTestReverseGatewayForResolve(t *testing.T) *mcpsrv.ReverseGateway {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	registry := edgeservices.NewInMemoryRegistry()
	servers := config.Servers{
		"app1": {
			Name:        "app1",
			Description: "app1",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
	}
	edgeCfg := config.EdgeConfig{
		Auth:    config.EdgeAuthPairing,
		Pairing: config.PairingConfig{Type: config.PairingTypeStatic},
	}.WithDefaults()
	gateway := mcpsrv.NewReverseGateway(registry, pairing, edgeCfg, servers)
	gateway.Init(t.Context())
	return gateway
}

func TestResolveMCPServer_EmptyPathValue_ReturnsNil(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	mcpSrv := mcpsrv.NewMCPServer(
		config.Servers{},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, mcpSrv.Init(t.Context()))

	got := resolveMCPServer(t.Context(), mcpSrv, newTestReverseGatewayForResolve(t), "")
	require.Nil(t, got)
}

func TestResolveMCPServer_ReverseServer_ResolvesFromGateway(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	mcpSrv := mcpsrv.NewMCPServer(
		config.Servers{},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, mcpSrv.Init(t.Context()))

	ctx := domainedge.WithIdentityKey(t.Context(), domainedge.StaticIdentityKey)
	got := resolveMCPServer(ctx, mcpSrv, newTestReverseGatewayForResolve(t), "app1")
	require.NotNil(t, got)
}

func TestResolveMCPServer_OpenAPIServer_ResolvesFromMCPServer(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "../internal/mcpsrv/fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	mcpSrv := mcpsrv.NewMCPServer(
		servers,
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, mcpSrv.Init(t.Context()))

	got := resolveMCPServer(t.Context(), mcpSrv, newTestReverseGatewayForResolve(t), "petstore")
	require.NotNil(t, got)
}

func TestResolveMCPServer_UnknownServer_ReturnsNil(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	mcpSrv := mcpsrv.NewMCPServer(
		config.Servers{},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, mcpSrv.Init(t.Context()))

	got := resolveMCPServer(t.Context(), mcpSrv, newTestReverseGatewayForResolve(t), "unknown")
	require.Nil(t, got)
}

func TestResolveMCPServer_NilReverseGateway_FallsBackToMCPServer(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "../internal/mcpsrv/fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	mcpSrv := mcpsrv.NewMCPServer(
		servers,
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
	)
	require.NoError(t, mcpSrv.Init(t.Context()))

	got := resolveMCPServer(t.Context(), mcpSrv, nil, "petstore")
	require.NotNil(t, got)
}

// --- mcpAuthMiddleware ---

// newTestReverseGatewayWithPairing builds a ReverseGateway declaring the
// reverse-transport server "app1" under the given pairing type.
func newTestReverseGatewayWithPairing(
	t *testing.T,
	pairingType config.PairingType,
) *mcpsrv.ReverseGateway {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	registry := edgeservices.NewInMemoryRegistry()
	servers := config.Servers{
		"app1": {
			Name:        "app1",
			Description: "app1",
			Transport:   config.MCPTransportReverse,
			Origin:      "https://app1.example.com",
		},
	}
	edgeCfg := config.EdgeConfig{
		Auth:    config.EdgeAuthPairing,
		Pairing: config.PairingConfig{Type: pairingType},
	}.WithDefaults()
	gateway := mcpsrv.NewReverseGateway(registry, pairing, edgeCfg, servers)
	gateway.Init(t.Context())
	return gateway
}

func newTestMCPAuthMux(
	t *testing.T,
	servers config.Servers,
	reverseGateway *mcpsrv.ReverseGateway,
	edgeCfg config.EdgeConfig,
	identityResolvers map[string]identity.Resolver,
) *httptest.Server {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle(
		"/mcp/{server_name}",
		mcpAuthMiddleware(servers, reverseGateway, edgeCfg, identityResolvers, "server_name")(next),
	)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPAuthMiddleware_StaticPairing_ReverseServer_NoAuthRequired(t *testing.T) {
	// edge.pairing.type=static の reverse サーバーには転送先バックエンドが無く、
	// パススルーの Bearer 存在チェックにも意味が無いため JWT を適用しない
	// (docs/design/webmcp-reverse-gateway.ja.md「type: static」)。
	servers := config.Servers{
		"app1": {
			Name:      "app1",
			Transport: config.MCPTransportReverse,
			Origin:    "https://app1.example.com",
		},
	}
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeStatic)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeStatic},
	}.WithDefaults()
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg, nil)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp/app1", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMCPAuthMiddleware_StaticPairing_NonReverseServer_RequiresBearer(t *testing.T) {
	// static pairing の JWT スキップは reverse サーバーに限る。他サーバーは従来どおり必須。
	servers := config.Servers{
		"app1": {
			Name:      "app1",
			Transport: config.MCPTransportReverse,
			Origin:    "https://app1.example.com",
		},
		"petstore": {Name: "petstore", OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeStatic)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeStatic},
	}.WithDefaults()
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg, nil)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		srv.URL+"/mcp/petstore",
		nil,
	)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// fakeIdentityResolver lets mcpAuthMiddleware tests target the
// identity-error-mapping branch (401/503/500) without a real credential
// source.
type fakeIdentityResolver struct {
	key domainedge.IdentityKey
	err error
}

func (r *fakeIdentityResolver) Resolve(
	context.Context,
	*http.Request,
) (domainedge.IdentityKey, error) {
	return r.key, r.err
}

func newTestRemoteReverseServers() config.Servers {
	return config.Servers{
		"app1": {
			Name:      "app1",
			Transport: config.MCPTransportReverse,
			Origin:    "https://app1.example.com",
			Identity:  "oauth",
		},
	}
}

func TestMCPAuthMiddleware_RemotePairing_ReverseServer_ValidCredential_SetsIdentityKeyAndProceeds(
	t *testing.T,
) {
	servers := newTestRemoteReverseServers()
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeRemote)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeRemote},
	}.WithDefaults()
	resolvers := map[string]identity.Resolver{
		"oauth": &fakeIdentityResolver{key: "oauth:user-a"},
	}
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg, resolvers)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp/app1", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMCPAuthMiddleware_RemotePairing_ReverseServer_Unauthenticated_401(t *testing.T) {
	servers := newTestRemoteReverseServers()
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeRemote)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeRemote},
	}.WithDefaults()
	resolvers := map[string]identity.Resolver{
		"oauth": &fakeIdentityResolver{err: identity.ErrUnauthenticated},
	}
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg, resolvers)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp/app1", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer resource_metadata=",
		"a 401 must challenge with the same scheme as middleware.JWT's pass-through path, "+
			"so an OAuth client can discover the auth flow via RFC 6750")
}

func TestMCPAuthMiddleware_RemotePairing_ReverseServer_Unavailable_503(t *testing.T) {
	servers := newTestRemoteReverseServers()
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeRemote)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeRemote},
	}.WithDefaults()
	resolvers := map[string]identity.Resolver{
		"oauth": &fakeIdentityResolver{err: identity.ErrUnavailable},
	}
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg, resolvers)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp/app1", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestMCPAuthMiddleware_RemotePairing_ReverseServer_NoResolverForProfile_500(t *testing.T) {
	// 設定バリデーション (pkg/config) が identity 参照の存在を保証するため通常起きないが、
	// 配線ミスに対する防御としてフェイルセーフに 500 を返す。
	servers := newTestRemoteReverseServers()
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeRemote)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeRemote},
	}.WithDefaults()
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg, map[string]identity.Resolver{})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp/app1", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// --- mcpAuthMiddleware + resolveMCPServer: per-identityKey routing (結合) ---
//
// Exercises the real config→identity.Resolver→mcpAuthMiddleware→
// ReverseGateway.ResolveServer chain against a self-hosted JWKS server and
// two jwt profiles, proving user isolation (a Bearer resolving to user A's
// server cannot reach user B's (identityKey, origin) binding) and identityKey
// stability across token rotation (same sub, new token → same binding).

// cmdBridgedFrameSender forwards a written frame's payload into the peer
// side's incoming channel, mirroring mcpsrv's own test helper of the same
// shape: it simulates one physical edge WebSocket connection carrying a
// binding's traffic without a real WebSocket.
type cmdBridgedFrameSender struct {
	peerIncoming chan json.RawMessage
}

func (s *cmdBridgedFrameSender) SendEdgeFrame(_ context.Context, frame mcpsrv.EdgeEnvelope) error {
	s.peerIncoming <- frame.Payload
	return nil
}

// bindFakeTab connects a fake WebMCP page exposing a single "read_dom" tool
// (returning resultText) to gateway under identityKey, as if a browser tab
// had paired and connected for that user.
func bindFakeTab(
	t *testing.T,
	gateway *mcpsrv.ReverseGateway,
	identityKey domainedge.IdentityKey,
	origin, resultText string,
) {
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
				Content: []mcp.Content{&mcp.TextContent{Text: resultText}},
			}, nil
		},
	)
	pageTransport := &mcpsrv.EdgeTransport{
		Origin:     origin,
		AppSession: "session-" + resultText,
		Sender:     &cmdBridgedFrameSender{peerIncoming: toGateway},
		Incoming:   toPage,
	}
	_, err := page.Connect(t.Context(), pageTransport, nil)
	require.NoError(t, err)

	sender := &cmdBridgedFrameSender{peerIncoming: toPage}
	require.NoError(t, gateway.HandleAppUp(
		t.Context(),
		[]domainedge.IdentityKey{identityKey},
		origin,
		"session-"+resultText,
		"conn-"+resultText,
		sender,
		toGateway,
	))
}

// newTestJWKSServer serves priv's public key as a single-key JWKS.
func newTestJWKSServer(t *testing.T, kid string, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	jwkKey, err := jwkset.NewJWKFromKey(pub, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: kid, ALG: jwkset.AlgRS256, USE: jwkset.UseSig},
	})
	require.NoError(t, err)
	body, err := json.Marshal(jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwkKey.Marshal()}})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPAuthMiddleware_RemotePairing_RoutesEachUserToTheirOwnBinding(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	const kid = "test-kid"
	jwksSrv := newTestJWKSServer(t, kid, &priv.PublicKey)

	const issuerA = "https://idp-a.example.com"
	const issuerB = "https://idp-b.example.com"
	sign := func(sub, issuer string) string {
		claims := jwt.MapClaims{
			"sub": sub, "iss": issuer,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = kid
		signed, err := token.SignedString(priv)
		require.NoError(t, err)
		return signed
	}

	profiles := map[string]*config.IdentityProfile{
		"profileA": {Source: config.IdentitySourceJWT, Issuer: issuerA, JWKSURL: jwksSrv.URL},
		"profileB": {Source: config.IdentitySourceJWT, Issuer: issuerB, JWKSURL: jwksSrv.URL},
	}
	resolvers, err := identity.NewResolvers(t.Context(), profiles, nil)
	require.NoError(t, err)

	servers := config.Servers{
		"app1": {
			Name: "app1", Description: "app1", Transport: config.MCPTransportReverse,
			Origin: "https://app1.example.com", Identity: "profileA",
		},
		"app2": {
			Name: "app2", Description: "app2", Transport: config.MCPTransportReverse,
			Origin: "https://app2.example.com", Identity: "profileB",
		},
	}
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	pairing := edgeservices.NewPairingService(storeClient)
	registry := edgeservices.NewInMemoryRegistry()
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeRemote},
	}.WithDefaults()
	gateway := mcpsrv.NewReverseGateway(registry, pairing, edgeCfg, servers)
	gateway.Init(t.Context()) // remote では何も事前構築しない

	tokenAX := sign("user-x", issuerA)
	tokenAY := sign("user-y", issuerA)
	tokenAX2 := sign("user-x", issuerA) // rotated token, same sub
	tokenBX := sign("user-x", issuerB)

	reqFor := func(pathServer, token string) *http.Request {
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodGet, "/mcp/"+pathServer, nil,
		)
		req.Header.Set("Authorization", "Bearer "+token)
		return req
	}
	keyAX, err := resolvers["profileA"].Resolve(t.Context(), reqFor("app1", tokenAX))
	require.NoError(t, err)
	keyAY, err := resolvers["profileA"].Resolve(t.Context(), reqFor("app1", tokenAY))
	require.NoError(t, err)
	keyBX, err := resolvers["profileB"].Resolve(t.Context(), reqFor("app2", tokenBX))
	require.NoError(t, err)

	bindFakeTab(t, gateway, keyAX, "https://app1.example.com", "dom-A-X")
	bindFakeTab(t, gateway, keyAY, "https://app1.example.com", "dom-A-Y")
	bindFakeTab(t, gateway, keyBX, "https://app2.example.com", "dom-B-X")

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv := resolveMCPServer(r.Context(), nil, gateway, r.PathValue("server_name"))
		if srv == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		callerTransport, serverTransport := mcp.NewInMemoryTransports()
		if _, err := srv.Connect(r.Context(), serverTransport, nil); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "0.0.1"}, nil)
		session, err := client.Connect(r.Context(), callerTransport, nil)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		defer session.Close()
		result, err := session.CallTool(r.Context(), &mcp.CallToolParams{Name: "read_dom"})
		if err != nil || result.IsError {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		text, _ := result.Content[0].(*mcp.TextContent)
		_, _ = w.Write([]byte(text.Text))
	})
	mux := http.NewServeMux()
	mux.Handle(
		"/mcp/{server_name}",
		mcpAuthMiddleware(servers, gateway, edgeCfg, resolvers, "server_name")(next),
	)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	doRequest := func(pathServer, token string) string {
		t.Helper()
		req, err := http.NewRequestWithContext(
			t.Context(), http.MethodGet, httpSrv.URL+"/mcp/"+pathServer, nil,
		)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close() //nolint: errcheck
		require.Equal(t, http.StatusOK, resp.StatusCode)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return string(b)
	}

	require.Equal(t, "dom-A-X", doRequest("app1", tokenAX))
	require.Equal(t, "dom-A-Y", doRequest("app1", tokenAY))
	require.Equal(
		t, "dom-A-X", doRequest("app1", tokenAX2),
		"same sub via a rotated token must reach the same binding",
	)
	require.Equal(
		t, "dom-B-X", doRequest("app2", tokenBX),
		"a different profile with the same sub must not collide with profileA's user-x",
	)
}

// TestNewHTTPHandler_WebSocketUpgradeSucceedsThroughLogging guards against a
// regression found during the WebMCP reverse gateway's manual E2E pass:
// middleware.Logging's responseWriter used to only embed http.ResponseWriter,
// which does not forward http.Hijacker, so /edge/ws could never hijack the
// connection to upgrade to WebSocket. All routes, including /edge/ws, now go
// through the same middleware chain (Logging included).
func TestNewHTTPHandler_WebSocketUpgradeSucceedsThroughLogging(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /edge/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		require.NoError(t, err)
		conn.Close(websocket.StatusNormalClosure, "")
	})

	handler := newHTTPHandler(mux)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/edge/ws"
	conn, resp, err := websocket.Dial(t.Context(), wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close() //nolint: errcheck
	}
	require.NoError(t, err, "/edge/ws should be able to hijack the connection through Logging")
	conn.CloseNow()
}

// TestNewHTTPHandler_Logs verifies that requests going through the shared
// middleware chain still produce the request/response log lines.
func TestNewHTTPHandler_Logs(t *testing.T) {
	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := newHTTPHandler(mux)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/healthz", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close() //nolint: errcheck

	require.Contains(t, logBuf.String(), "http request")
	require.Contains(t, logBuf.String(), "http response")
}

func TestNewGatewayCmd(t *testing.T) {
	cmd := newGatewayCmd()
	require.Equal(t, "gateway", cmd.Use)
	require.Equal(t, "Start mcp gateway server", cmd.Short)
	require.NotNil(t, cmd.RunE)
}

func TestRunServer_GracefulShutdown(t *testing.T) {
	// httptest でランダムポートのサーバーを作成
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// テスト用HTTPサーバーをランダムポートで起動
	ts := httptest.NewServer(mux)
	ts.Close() // すぐに閉じる（ポートだけ取得）

	srv := &http.Server{
		Addr:    "127.0.0.1:0",
		Handler: mux,
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- runServer(ctx, srv, "test-server", 0, "", "")
	}()

	// サーバーが起動するのを少し待ってからキャンセル
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		// グレースフルシャットダウンはエラーなし
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runServer did not return in time")
	}
}

func TestRunServer_ServerError(t *testing.T) {
	// すでに使用中のポートでサーバーを起動しようとするとエラー
	// まず既存サーバーでポートを使用
	listener := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
	)
	defer listener.Close()

	addr := listener.Listener.Addr().String()
	srv := &http.Server{
		Addr:    addr,
		Handler: http.DefaultServeMux,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := runServer(ctx, srv, "test-server", 0, "", "")
	// ポートが使用中のためエラーが返る
	require.Error(t, err)
	require.Contains(t, err.Error(), "test-server error")
}
