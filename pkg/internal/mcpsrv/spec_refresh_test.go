package mcpsrv

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/stretchr/testify/require"
)

// specTestServer serves an OpenAPI spec whose body and status can be swapped
// while the test is running, standing in for a spec that changes upstream.
// fetches counts requests so a test can assert that refreshing has stopped.
type specTestServer struct {
	*httptest.Server

	mu      sync.Mutex
	body    string
	status  int
	fetches atomic.Int64
}

func newSpecTestServer(t *testing.T, body string) *specTestServer {
	t.Helper()
	s := &specTestServer{body: body, status: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.fetches.Add(1)
		s.mu.Lock()
		body, status := s.body, s.status
		s.mu.Unlock()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *specTestServer) setBody(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = body
}

func (s *specTestServer) setStatus(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func specWithOperations(operationIDs ...string) string {
	paths := make([]string, 0, len(operationIDs))
	for _, id := range operationIDs {
		paths = append(paths, fmt.Sprintf(
			`"/%s": {"get": {"operationId": "%s", "responses": {"200": {"description": "ok"}}}}`,
			id, id,
		))
	}
	return fmt.Sprintf(
		`{"openapi":"3.0.0","info":{"title":"test","version":"1.0.0"},"paths":{%s}}`,
		strings.Join(paths, ","),
	)
}

func newRefreshTestMCPServer(
	t *testing.T,
	spec *specTestServer,
	interval *time.Duration,
) *MCPServer {
	t.Helper()
	servers := config.Servers{
		"api": &config.Server{
			Name:                "api",
			Description:         "test api",
			Spec:                spec.URL + "/openapi.json",
			BaseURL:             spec.URL,
			SpecRefreshInterval: interval,
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))
	return s
}

// tryListToolNames は require を使わないため、require.Eventually の条件関数
// （テスト本体とは別の goroutine で実行される）からも呼べる。
func tryListToolNames(ctx context.Context, srv *mcp.Server) ([]string, error) {
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverTransport, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer session.Close() //nolint: errcheck

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names, nil
}

func listToolNames(t *testing.T, srv *mcp.Server) []string {
	t.Helper()
	names, err := tryListToolNames(t.Context(), srv)
	require.NoError(t, err)
	return names
}

func TestMCPServer_RefreshServer_UnchangedSpec(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	s := newRefreshTestMCPServer(t, spec, nil)

	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.False(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_AddedOperation(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	s := newRefreshTestMCPServer(t, spec, nil)

	spec.setBody(specWithOperations("ping", "pong"))
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_RemovedOperation(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping", "pong"))
	s := newRefreshTestMCPServer(t, spec, nil)

	spec.setBody(specWithOperations("ping"))
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_FetchError_KeepsTools(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	s := newRefreshTestMCPServer(t, spec, nil)

	spec.setStatus(http.StatusInternalServerError)
	changed, err := s.refreshServer(t.Context(), "api")
	require.Error(t, err)
	require.False(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_UnknownServer(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	s := newRefreshTestMCPServer(t, spec, nil)

	_, err := s.refreshServer(t.Context(), "nonexistent")
	require.Error(t, err)
}

func TestMCPServer_RefreshServer_MCPBackendMode_NotRefreshable(t *testing.T) {
	servers := config.Servers{
		"backend": &config.Server{
			Name:      "backend",
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))

	_, err := s.refreshServer(t.Context(), "backend")
	require.Error(t, err)
}

func TestMCPServer_StartSpecRefresh_UpdatesToolsAndStopsOnClose(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	interval := 20 * time.Millisecond
	s := newRefreshTestMCPServer(t, spec, &interval)

	s.StartSpecRefresh(t.Context(), 0)
	spec.setBody(specWithOperations("ping", "pong"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		names, err := tryListToolNames(t.Context(), srv)
		return err == nil && len(names) == 2
	}, 5*time.Second, 20*time.Millisecond)

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the spec refresh goroutines")
	}

	fetches := spec.fetches.Load()
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, fetches, spec.fetches.Load(), "no spec fetch should happen after Close")
}

func TestMCPServer_StartSpecRefresh_DisabledInterval_NeverRefreshes(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	disabled := time.Duration(0)
	s := newRefreshTestMCPServer(t, spec, &disabled)

	// グローバル既定が正でも、サーバー側の 0 指定が優先されリフレッシュしない。
	s.StartSpecRefresh(t.Context(), 20*time.Millisecond)
	defer s.Close()

	fetches := spec.fetches.Load()
	spec.setBody(specWithOperations("ping", "pong"))
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, fetches, spec.fetches.Load())
}

func TestMCPServer_StartSpecRefresh_GlobalInterval(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	s := newRefreshTestMCPServer(t, spec, nil)

	s.StartSpecRefresh(t.Context(), 20*time.Millisecond)
	defer s.Close()
	spec.setBody(specWithOperations("ping", "pong"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		names, err := tryListToolNames(t.Context(), srv)
		return err == nil && len(names) == 2
	}, 5*time.Second, 20*time.Millisecond)
}
