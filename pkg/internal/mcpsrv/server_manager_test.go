package mcpsrv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/stretchr/testify/require"
)

// fakeMediaService は Enabled() を true にした状態での Do() 呼び出し内容を検証するためのテスト用スタブ。
type fakeMediaService struct {
	enabled bool
	doFunc  func(ctx context.Context, data []byte, contentType string) (string, string, error)
}

func (f *fakeMediaService) SaveContent(
	ctx context.Context,
	data []byte,
	contentType string,
) (string, string, error) {
	return f.doFunc(ctx, data, contentType)
}

func (f *fakeMediaService) AccessCheck(ctx context.Context) error { return nil }

func (f *fakeMediaService) Enabled() bool { return f.enabled }

func (f *fakeMediaService) DownloadContent(
	ctx context.Context,
	id string,
) (io.ReadCloser, string, error) {
	return nil, "", nil
}

func TestGenerateContent_BinaryImage_NoopUploader(t *testing.T) {
	raw := []byte("raw-image-bytes")
	encoded := []byte(base64.URLEncoding.EncodeToString(raw))

	// mediaUploader が無効の場合、デコードはバックエンドサービス側の責務ではないため
	// base64 のまま ImageContent.Data に格納される。
	contents, err := generateContent(
		context.Background(),
		"image/png",
		encoded,
		storage.NewNoopUploader(),
	)
	require.NoError(t, err)
	require.Len(t, contents, 1)

	img, ok := contents[0].(*mcp.ImageContent)
	require.True(t, ok)
	require.Equal(t, encoded, img.Data)
}

func TestGenerateContent_BinaryImage_EnabledUploader(t *testing.T) {
	raw := []byte("raw-image-bytes")
	encoded := []byte(base64.URLEncoding.EncodeToString(raw))

	var gotData []byte
	mediaService := &fakeMediaService{
		enabled: true,
		doFunc: func(ctx context.Context, data []byte, contentType string) (string, string, error) {
			gotData = data
			return "media-id", "https://example.com/media-id", nil
		},
	}

	// generateContent 自体はデコードを行わず、そのまま MediaUploadService.Do に渡す。
	// デコード（と失敗時のフォールバック）は storage.MediaUpload.Do が担う。
	contents, err := generateContent(context.Background(), "image/png", encoded, mediaService)
	require.NoError(t, err)
	require.Len(t, contents, 1)

	link, ok := contents[0].(*mcp.ResourceLink)
	require.True(t, ok)
	require.Equal(t, "media-id", link.Name)
	require.Equal(t, encoded, gotData)
}

// 上流 API が application/octet-stream しか返さない場合でも、実体から型を判定して
// resource_link に載せる。受け手（Claude Code）は mimeType が octet-stream のままだと
// 画像なのか文書なのかを判別できない。
func TestGenerateContent_OctetStream_DetectsTypeFromBody(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000000000000000")
	encoded := []byte(base64.URLEncoding.EncodeToString(png))

	var gotContentType string
	mediaService := &fakeMediaService{
		enabled: true,
		doFunc: func(ctx context.Context, data []byte, contentType string) (string, string, error) {
			gotContentType = contentType
			return "media-id", "https://example.com/media-id", nil
		},
	}

	contents, err := generateContent(
		context.Background(),
		"application/octet-stream",
		encoded,
		mediaService,
	)
	require.NoError(t, err)
	require.Len(t, contents, 1)

	link, ok := contents[0].(*mcp.ResourceLink)
	require.True(t, ok)
	require.Equal(t, "image/png", link.MIMEType)
	require.Equal(t, "image/png", gotContentType)
}

// mimeType フィールドは受け手がテキストへ変換する過程で落ちるため、説明文にも
// Content-Type を書いておく。説明文は変換後も残る。
func TestGenerateContent_ResourceLinkDescriptionCarriesContentType(t *testing.T) {
	encoded := []byte(base64.URLEncoding.EncodeToString([]byte("raw-image-bytes")))

	mediaService := &fakeMediaService{
		enabled: true,
		doFunc: func(ctx context.Context, data []byte, contentType string) (string, string, error) {
			return "media-id", "https://example.com/media-id", nil
		},
	}

	contents, err := generateContent(context.Background(), "image/png", encoded, mediaService)
	require.NoError(t, err)
	require.Len(t, contents, 1)

	link, ok := contents[0].(*mcp.ResourceLink)
	require.True(t, ok)
	require.Contains(t, link.Description, "Content-Type: image/png")
}

// アップロード先が無効な場合も、判定した型で ImageContent に振り分ける
// （octet-stream のままだと default 分岐に落ちて TextContent になってしまう）。
func TestGenerateContent_OctetStream_NoopUploader_RoutesByDetectedType(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000000000000000")
	encoded := []byte(base64.URLEncoding.EncodeToString(png))

	contents, err := generateContent(
		context.Background(),
		"application/octet-stream",
		encoded,
		storage.NewNoopUploader(),
	)
	require.NoError(t, err)
	require.Len(t, contents, 1)

	img, ok := contents[0].(*mcp.ImageContent)
	require.True(t, ok)
	require.Equal(t, "image/png", img.MIMEType)
}

func TestNewMCPServer(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{Spec: "fixtures/petstore_oas.json"},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NotNil(t, s)
	require.NotNil(t, s.mediaUploader)
	require.NotNil(t, s.srv)
	require.NotNil(t, s.appSrv)
	require.NotNil(t, s.backendClients)
}

func TestMCPServer_Init_OpenAPIMode(t *testing.T) {
	servers := config.Servers{
		"petstore": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("petstore")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestMCPServer_Init_SwaggerMode(t *testing.T) {
	servers := config.Servers{
		"swagger": &config.Server{
			Spec:    "fixtures/petstore_swagger.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	srv, err := s.Server("swagger")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestMCPServer_Init_MCPBackendMode(t *testing.T) {
	servers := config.Servers{
		"backend": &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// MCP バックエンドモードのサーバーも appSrv に登録される
	srv, err := s.Server("backend")
	require.NoError(t, err)
	require.NotNil(t, srv)

	// バックエンドクライアントも登録される
	bc, ok := s.BackendClient("backend")
	require.True(t, ok)
	require.NotNil(t, bc)
}

func TestMCPServer_Init_InvalidSpec(t *testing.T) {
	servers := config.Servers{
		"invalid": &config.Server{
			Spec: "fixtures/nonexistent.json",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.Error(t, err)
}

func TestMCPServer_Server_NotFound(t *testing.T) {
	servers := config.Servers{}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	_ = s.Init(context.Background())

	_, err := s.Server("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found mcp server")
}

func TestMCPServer_BackendClient_NotFound(t *testing.T) {
	servers := config.Servers{}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	_ = s.Init(context.Background())

	bc, ok := s.BackendClient("nonexistent")
	require.False(t, ok)
	require.Nil(t, bc)
}

func TestMCPServer_Close_NoBackends(t *testing.T) {
	servers := config.Servers{
		"openapi": &config.Server{Spec: "fixtures/petstore_oas.json"},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// バックエンドがない場合も Close はパニックしない
	require.NotPanics(t, func() {
		s.Close()
	})
}

func TestMCPServer_Close_WithBackend(t *testing.T) {
	servers := config.Servers{
		"backend": &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// 接続していないバックエンドを Close してもパニックしない
	require.NotPanics(t, func() {
		s.Close()
	})
}

func TestMCPServer_Init_ReverseServer_NotManagedByMCPServer(t *testing.T) {
	// reverse サーバーは ReverseGateway が別途処理するため、MCPServer 自身は
	// appSrv/backendClients のどちらにも登録しない。
	servers := config.Servers{
		"app1": &config.Server{
			Transport: config.MCPTransportReverse,
			Origin:    "https://app1.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	_, err = s.Server("app1")
	require.Error(t, err)

	_, ok := s.BackendClient("app1")
	require.False(t, ok)
}

// --- WithServerMiddleware ---

// recordingMiddleware appends name to calls every time it runs, letting a
// test assert both that it ran and which server name it was built for.
// recordingMiddleware records name only for tools/list requests, ignoring
// the initialize handshake methods every client connection also sends
// through the same receiving middleware chain.
func recordingMiddleware(name string, calls *[]string) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/list" {
				*calls = append(*calls, name)
			}
			return next(ctx, method, req)
		}
	}
}

func TestMCPServer_WithServerMiddleware_AppliedToEachServer(t *testing.T) {
	servers := config.Servers{
		"oas": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")

	var calls []string
	s := NewMCPServer(
		servers,
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
		WithServerMiddleware(func(name string) []mcp.Middleware {
			return []mcp.Middleware{recordingMiddleware(name, &calls)}
		}),
	)
	require.NoError(t, s.Init(context.Background()))

	srv, err := s.Server("oas")
	require.NoError(t, err)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = srv.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	defer session.Close() //nolint: errcheck

	_, err = session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"oas"}, calls)
}

func TestMCPServer_NoMiddlewareOption_LeavesServerUnaffected(t *testing.T) {
	servers := config.Servers{
		"oas": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))

	srv, err := s.Server("oas")
	require.NoError(t, err)
	require.NotNil(t, srv)
}

func TestMCPServer_Init_MultipleServers(t *testing.T) {
	servers := config.Servers{
		"oas": &config.Server{
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore1.example.com",
		},
		"swagger": &config.Server{
			Spec:    "fixtures/petstore_swagger.json",
			BaseURL: "https://petstore2.example.com",
		},
		"mcp": &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://mcp.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	err := s.Init(context.Background())
	require.NoError(t, err)

	// 全サーバーが登録されている
	for name := range servers {
		srv, err := s.Server(name)
		require.NoError(t, err, "server %s should be registered", name)
		require.NotNil(t, srv)
	}

	// MCP バックエンドのみ BackendClient がある
	bc, ok := s.BackendClient("mcp")
	require.True(t, ok)
	require.NotNil(t, bc)

	_, ok = s.BackendClient("oas")
	require.False(t, ok)
}

// --- MCPServer.ToolCatalog ---

func TestMCPServer_ToolCatalog_OpenAPIMode(t *testing.T) {
	servers := config.Servers{
		"petstore": &config.Server{
			Name:    "petstore",
			Spec:    "fixtures/petstore_oas.json",
			BaseURL: "https://petstore.example.com",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))

	tools, err := s.ToolCatalog(context.Background(), "petstore")
	require.NoError(t, err)
	require.Contains(t, tools, ToolInfo{Name: "getpetbyid", Description: "Find pet by ID."})
}

func TestMCPServer_ToolCatalog_UnknownServer(t *testing.T) {
	servers := config.Servers{}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))

	_, err := s.ToolCatalog(context.Background(), "nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found mcp server")
}

// newToolCatalogBackendServer は接続ハンドシェイク（旧プロトコルの initialize、
// 新プロトコルの server/discover）の回数を数えるバックエンドを返す。
// 接続が共有されているかの検証に使う。
func newToolCatalogBackendServer(
	t *testing.T,
	requestDelay time.Duration,
) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "ping",
			Description: "ping the backend",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	initializeCalls := &atomic.Int32{}
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		var rpc struct {
			Method string `json:"method"`
		}
		if json.Unmarshal(body, &rpc) == nil &&
			(rpc.Method == "initialize" || rpc.Method == "server/discover") {
			initializeCalls.Add(1)
		}
		time.Sleep(requestDelay)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(httpSrv.Close)
	return httpSrv, initializeCalls
}

func TestMCPServer_ToolCatalog_MCPBackendMode_ConnectsAndReturnsTools(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, _ := newToolCatalogBackendServer(t, 0)

	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       httpSrv.URL,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))

	tools, err := s.ToolCatalog(context.Background(), "backend")
	require.NoError(t, err)
	require.Equal(t, []ToolInfo{{Name: "ping", Description: "ping the backend"}}, tools)
}

func TestMCPServer_ToolCatalog_MCPBackendMode_ConcurrentRequestsShareConnection(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, initializeCalls := newToolCatalogBackendServer(t, 25*time.Millisecond)

	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       httpSrv.URL,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)

	const callers = 16
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tools, err := s.ToolCatalog(context.Background(), "backend")
			if err == nil && len(tools) != 1 {
				err = fmt.Errorf("unexpected tool count: %d", len(tools))
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	// 16 並行の初回呼び出しでも、バックエンドへの接続（initialize）は 1 回だけ。
	// tools/list は毎回転送されるため総リクエスト数ではなく initialize で検証する。
	require.Equal(t, int32(1), initializeCalls.Load())
}

// newGatedBackendServer は release が呼ばれるまで全リクエストをブロックするバックエンドを返す。
// failOnRelease の場合、解放後のリクエストは HTTP 500 で失敗する。
func newGatedBackendServer(
	t *testing.T,
	failOnRelease bool,
) (*httptest.Server, *atomic.Int32, func()) {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "backend", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{
			Name:        "ping",
			Description: "ping the backend",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
		},
	)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	requestCalls := &atomic.Int32{}
	gate := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCalls.Add(1)
		<-gate
		if failOnRelease {
			http.Error(w, "backend down", http.StatusInternalServerError)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	// Cleanup は LIFO なので、httpSrv.Close の前に release が走りハンドラの詰まりを解く。
	t.Cleanup(httpSrv.Close)
	t.Cleanup(release)
	return httpSrv, requestCalls, release
}

func newSingleBackendMCPServer(t *testing.T, backendURL string) *MCPServer {
	t.Helper()
	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       backendURL,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))
	t.Cleanup(s.Close)
	return s
}

func TestMCPBackendClient_EnsureConnected_WaiterHonorsContext(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, requestCalls, release := newGatedBackendServer(t, false)
	s := newSingleBackendMCPServer(t, httpSrv.URL)
	bc, ok := s.BackendClient("backend")
	require.True(t, ok)

	leaderCtx, leaderCancel := context.WithCancel(context.Background())
	defer leaderCancel()
	leaderErr := make(chan error, 1)
	go func() { leaderErr <- bc.EnsureConnected(leaderCtx) }()
	require.Eventually(t, func() bool { return requestCalls.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "leader should start connecting")

	// 接続試行が進行中でも、待機者は自分の ctx の期限で抜けられる
	waitCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := bc.EnsureConnected(waitCtx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	release()
	require.NoError(t, <-leaderErr)
	require.NoError(t, bc.EnsureConnected(context.Background()))
}

func TestMCPBackendClient_Close_DoesNotBlockOnInflightConnect(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, requestCalls, release := newGatedBackendServer(t, false)
	s := newSingleBackendMCPServer(t, httpSrv.URL)
	bc, ok := s.BackendClient("backend")
	require.True(t, ok)

	leaderErr := make(chan error, 1)
	go func() { leaderErr <- bc.EnsureConnected(context.Background()) }()
	require.Eventually(t, func() bool { return requestCalls.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "leader should start connecting")

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close should return without waiting for the in-flight connect")
	}

	// 接続完了後もセッションは採用されず、以降の接続要求は拒否される
	release()
	require.ErrorContains(t, <-leaderErr, "client closed")
	require.ErrorContains(t, bc.EnsureConnected(context.Background()), "client closed")
}

func TestMCPBackendClient_EnsureConnected_WaitersShareLeaderFailure(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, requestCalls, release := newGatedBackendServer(t, true)
	s := newSingleBackendMCPServer(t, httpSrv.URL)
	bc, ok := s.BackendClient("backend")
	require.True(t, ok)

	leaderErr := make(chan error, 1)
	go func() { leaderErr <- bc.EnsureConnected(context.Background()) }()
	require.Eventually(t, func() bool { return requestCalls.Load() >= 1 },
		5*time.Second, 5*time.Millisecond, "leader should start connecting")

	const waiters = 8
	errs := make(chan error, waiters)
	for range waiters {
		go func() { errs <- bc.EnsureConnected(context.Background()) }()
	}
	// 待機者が進行中の試行に合流するのを待ってから失敗させる
	time.Sleep(250 * time.Millisecond)
	release()

	require.ErrorContains(t, <-leaderErr, "connect")
	for range waiters {
		require.ErrorContains(t, <-errs, "connect")
	}
	sharedPhaseCalls := requestCalls.Load()

	// 1 試行あたりの HTTP リクエスト数は SDK の内部リトライで変わるため、
	// 単独の失敗試行を実測してベースラインにする。
	require.ErrorContains(t, bc.EnsureConnected(context.Background()), "connect")
	singleAttemptCalls := requestCalls.Load() - sharedPhaseCalls
	require.Positive(t, singleAttemptCalls)

	// リーダー+待機者 9 呼び出しでもバックエンドに到達するのは 1 試行分のみ。
	// 待機者は失敗を共有し、各自が接続をやり直さない。
	require.Equal(t, singleAttemptCalls, sharedPhaseCalls)
}

// TestMCPServer_MCPBackendMode_ToolsPassthrough は、MCP バックエンドの
// tools/list・tools/call がゲートウェイのレジストリを介さず毎回バックエンドへ
// 転送されること（後から追加されたツールが即時見える・呼べる）を検証する。
func TestMCPServer_MCPBackendMode_ToolsPassthrough(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	httpSrv, backendSrv := newBackendCatalogServer(t)
	s := newSingleBackendMCPServer(t, httpSrv.URL)

	gwSrv, err := s.Server("backend")
	require.NoError(t, err)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	_, err = gwSrv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer session.Close()

	toolNames := func(tools []*mcp.Tool) []string {
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Name
		}
		return names
	}

	result, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Equal(t, []string{"ping"}, toolNames(result.Tools))

	// 接続後にバックエンドへ追加されたツールが tools/list に即時反映され、呼び出せる
	backendSrv.AddTool(
		&mcp.Tool{
			Name:        "echo",
			Description: "echo the input",
			InputSchema: map[string]any{"type": "object"},
		},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "echoed"}},
			}, nil
		},
	)

	result, err = session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	require.Contains(t, toolNames(result.Tools), "echo")

	callResult, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "echo",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)
	text, ok := callResult.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Equal(t, "echoed", text.Text)
}

func TestMCPServer_ToolCatalog_MCPBackendMode_ConnectError(t *testing.T) {
	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       "http://127.0.0.1:1/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(context.Background()))

	_, err := s.ToolCatalog(context.Background(), "backend")
	require.Error(t, err)
}
