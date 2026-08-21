package cmd

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/sqlite"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/mcpsrv"
	edgeservices "github.com/nonchan7720/manifold/pkg/services/edge"
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

	got := resolveMCPServer(t.Context(), mcpSrv, newTestReverseGatewayForResolve(t), "app1")
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
) *httptest.Server {
	t.Helper()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle(
		"/mcp/{server_name}",
		mcpAuthMiddleware(servers, reverseGateway, edgeCfg, "server_name")(next),
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
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg)

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
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg)

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

func TestMCPAuthMiddleware_RemotePairing_ReverseServer_RequiresBearer(t *testing.T) {
	// remote pairing では Manifold 自身の署名検証が必要 (将来対応)。今回のスコープでは
	// 少なくとも従来どおり Bearer 必須のままであること。
	servers := config.Servers{
		"app1": {
			Name:      "app1",
			Transport: config.MCPTransportReverse,
			Origin:    "https://app1.example.com",
		},
	}
	gateway := newTestReverseGatewayWithPairing(t, config.PairingTypeRemote)
	edgeCfg := config.EdgeConfig{
		Pairing: config.PairingConfig{Type: config.PairingTypeRemote},
	}.WithDefaults()
	srv := newTestMCPAuthMux(t, servers, gateway, edgeCfg)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/mcp/app1", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint: errcheck

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
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
