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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
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
	opts ...Option,
) *MCPServer {
	t.Helper()
	return newRefreshTestMCPServerWith(t, spec, func(srv *config.Server) {
		srv.SpecRefreshInterval = interval
	}, opts...)
}

func newRefreshTestMCPServerWith(
	t *testing.T,
	spec *specTestServer,
	configure func(*config.Server),
	opts ...Option,
) *MCPServer {
	t.Helper()
	server := &config.Server{
		Name:        "api",
		Description: "test api",
		Spec:        spec.URL + "/openapi.json",
		BaseURL:     spec.URL,
	}
	configure(server)
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(
		config.Servers{"api": server},
		storage.NewContentManagementService(u, storage.NewNoopUploader()),
		opts...,
	)
	require.NoError(t, s.Init(t.Context()))
	return s
}

// withRejectOn sets mcpServers.api.specRefreshRejectOn.
func withRejectOn(level string) func(*config.Server) {
	return func(srv *config.Server) { srv.SpecRefreshRejectOn = &level }
}

// newTestMeterProvider returns a MeterProvider whose metrics can be read back
// with counterValues.
func newTestMeterProvider(t *testing.T) (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	return mp, reader
}

// counterValues returns the int64 counter name's values keyed by its "level"
// attribute.
func counterValues(t *testing.T, reader *sdkmetric.ManualReader, name string) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	values := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %s is not an int64 sum", name)
			for _, dp := range sum.DataPoints {
				server, _ := dp.Attributes.Value("server")
				require.Equal(t, "api", server.AsString())
				level, _ := dp.Attributes.Value("level")
				values[level.AsString()] += dp.Value
			}
		}
	}
	return values
}

// counterReasons is counterValues keyed by the "reason" attribute.
func counterReasons(t *testing.T, reader *sdkmetric.ManualReader, name string) map[string]int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	values := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			require.True(t, ok, "metric %s is not an int64 sum", name)
			for _, dp := range sum.DataPoints {
				reason, _ := dp.Attributes.Value("reason")
				values[reason.AsString()] += dp.Value
			}
		}
	}
	return values
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

// baseURL 未設定で spec の URL から導出している場合、refresh で servers にホストの無い
// URL が入っても採用せず、同じリビジョンの間は再び WARN（エラー）を返さないこと。
func TestMCPServer_RefreshServer_UnresolvableBaseURL_RejectedOnce(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	mp, reader := newTestMeterProvider(t)
	s := newRefreshTestMCPServerWith(t, spec, func(srv *config.Server) {
		srv.BaseURL = "" // spec の URL（http）から導出させる
	}, WithMeterProvider(mp))
	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))

	broken := strings.Replace(
		specWithOperations("ping", "pong"),
		`"paths"`, `"servers":[{"url":"api.example.com"}],"paths"`, 1,
	)
	spec.setBody(broken)
	changed, err := s.refreshServer(t.Context(), "api")
	require.ErrorIs(t, err, errBaseURLUnresolved)
	require.False(t, changed)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv), "current tools are kept")
	require.Equal(t, map[string]int64{"": 1},
		counterValues(t, reader, "manifold.openapi.spec_refresh.rejected"),
		"counted as a rejection (no breaking-change level)")
	require.Equal(t, map[string]int64{rejectReasonBaseURL: 1},
		counterReasons(t, reader, "manifold.openapi.spec_refresh.rejected"))

	changed, err = s.refreshServer(t.Context(), "api")
	require.NoError(t, err, "the same rejected revision is not reported again")
	require.False(t, changed)
	require.Equal(t, map[string]int64{rejectReasonBaseURL: 1},
		counterReasons(t, reader, "manifold.openapi.spec_refresh.rejected"), "not counted again")

	spec.setBody(specWithOperations("ping", "pong"))
	changed, err = s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed, "a fixed spec is adopted")
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_UpdatesToolCatalogDescriptions(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	s := newRefreshTestMCPServer(t, spec, nil)

	catalog, err := s.ToolCatalog(t.Context(), "api")
	require.NoError(t, err)
	// specWithOperations emits neither summary nor description, so the catalog
	// reports both as "" rather than the "GET /ping" MCP fallback description.
	require.Equal(t, []ToolInfo{{Name: "ping"}}, catalog)

	spec.setBody(specWithOperations("ping", "pong"))
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)

	catalog, err = s.ToolCatalog(t.Context(), "api")
	require.NoError(t, err)
	require.ElementsMatch(t, []ToolInfo{{Name: "ping"}, {Name: "pong"}}, catalog)
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

	s.StartSpecRefresh(t.Context(), config.SpecRefreshConfig{})
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

	// Close cancels the refresh goroutine's context and waits for it to
	// return, but the in-flight request's context-canceled error can still
	// reach the httptest handler (which increments fetches) just after Close
	// returns. Wait for the counter to stabilize before taking the baseline,
	// otherwise it flakes on a fetch that was already in flight at Close time.
	var fetches int64
	require.Eventually(t, func() bool {
		before := spec.fetches.Load()
		time.Sleep(20 * time.Millisecond)
		after := spec.fetches.Load()
		fetches = after
		return before == after
	}, 2*time.Second, 20*time.Millisecond)

	time.Sleep(200 * time.Millisecond)
	require.Equal(t, fetches, spec.fetches.Load(), "no spec fetch should happen after Close")
}

func TestMCPServer_StartSpecRefresh_DisabledInterval_NeverRefreshes(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	disabled := time.Duration(0)
	s := newRefreshTestMCPServer(t, spec, &disabled)

	// グローバル既定が正でも、サーバー側の 0 指定が優先されリフレッシュしない。
	s.StartSpecRefresh(t.Context(), config.SpecRefreshConfig{Interval: 20 * time.Millisecond})
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

	s.StartSpecRefresh(t.Context(), config.SpecRefreshConfig{Interval: 20 * time.Millisecond})
	defer s.Close()
	spec.setBody(specWithOperations("ping", "pong"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		names, err := tryListToolNames(t.Context(), srv)
		return err == nil && len(names) == 2
	}, 5*time.Second, 20*time.Millisecond)
}

func TestMCPServer_StartSpecRefresh_CalledTwice_StopsPreviousCycle(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	interval := 20 * time.Millisecond
	s := newRefreshTestMCPServer(t, spec, &interval)

	s.StartSpecRefresh(t.Context(), config.SpecRefreshConfig{})
	s.StartSpecRefresh(t.Context(), config.SpecRefreshConfig{})

	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal(
			"Close did not stop the spec refresh goroutines started by the previous StartSpecRefresh",
		)
	}

	fetches := spec.fetches.Load()
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, fetches, spec.fetches.Load(), "no spec fetch should happen after Close")
}

func TestMCPServer_RefreshServer_NonBreakingChange_Adopted(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	mp, reader := newTestMeterProvider(t)
	s := newRefreshTestMCPServerWith(t, spec, withRejectOn("WARN"), WithMeterProvider(mp))

	// operation の追加は info レベル（非破壊的）なので rejectOn=WARN でも採用される。
	spec.setBody(specWithOperations("ping", "pong"))
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))
	require.Equal(t, map[string]int64{"info": 1},
		counterValues(t, reader, "manifold.openapi.spec_refresh.changes"))
	require.Empty(t, counterValues(t, reader, "manifold.openapi.spec_refresh.rejected"))
}

func TestMCPServer_RefreshServer_BreakingChange_AdoptedWithoutRejectOn(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping", "pong"))
	mp, reader := newTestMeterProvider(t)
	s := newRefreshTestMCPServer(t, spec, nil, WithMeterProvider(mp))

	// operation の削除は error レベルの破壊的変更だが、rejectOn 未設定なので採用される。
	spec.setBody(specWithOperations("ping"))
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
	// パスの削除は error に加え info の変更も伴う。
	require.Equal(t, int64(1),
		counterValues(t, reader, "manifold.openapi.spec_refresh.changes")["error"])
	require.Empty(t, counterValues(t, reader, "manifold.openapi.spec_refresh.rejected"))
}

func TestMCPServer_RefreshServer_BreakingChange_Rejected(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping", "pong"))
	mp, reader := newTestMeterProvider(t)
	s := newRefreshTestMCPServerWith(t, spec, withRejectOn("ERR"), WithMeterProvider(mp))

	spec.setBody(specWithOperations("ping"))
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.False(t, changed)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))
	catalog, err := s.ToolCatalog(t.Context(), "api")
	require.NoError(t, err)
	require.Len(t, catalog, 2)
	// パスの削除は error に加え info の変更も伴う。
	require.Equal(t, int64(1),
		counterValues(t, reader, "manifold.openapi.spec_refresh.changes")["error"])
	require.Equal(t, map[string]int64{"error": 1},
		counterValues(t, reader, "manifold.openapi.spec_refresh.rejected"))

	// 上流が直すまで同じ spec は拒否され続けるが、diff・計上し直しはしない。
	changed, err = s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.False(t, changed)
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))
	require.Equal(t, map[string]int64{"error": 1},
		counterValues(t, reader, "manifold.openapi.spec_refresh.rejected"))

	// 上流が直せば、提供中の（拒否前の）spec に対して改めて判定され採用される。
	spec.setBody(specWithOperations("ping", "pong", "extra"))
	changed, err = s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)
	require.ElementsMatch(t, []string{"ping", "pong", "extra"}, listToolNames(t, srv))
}

func TestMCPServer_StartSpecRefresh_GlobalRejectOn_KeepsTools(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping", "pong"))
	mp, reader := newTestMeterProvider(t)
	s := newRefreshTestMCPServer(t, spec, nil, WithMeterProvider(mp))

	s.StartSpecRefresh(t.Context(), config.SpecRefreshConfig{
		Interval: 20 * time.Millisecond,
		RejectOn: "ERR",
	})
	defer s.Close()
	spec.setBody(specWithOperations("ping"))

	require.Eventually(t, func() bool {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &rm); err != nil {
			return false
		}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "manifold.openapi.spec_refresh.rejected" {
					return true
				}
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_InitialFetchFailed_NoBaseToCheck(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	spec := newSpecTestServer(t, specWithOperations("ping"))
	spec.setStatus(http.StatusInternalServerError)
	mp, reader := newTestMeterProvider(t)
	s := newRefreshTestMCPServerWith(t, spec, withRejectOn("INFO"), WithMeterProvider(mp))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Empty(t, listToolNames(t, srv))

	// 比較元の spec が無いので、rejectOn に関わらずそのまま採用される。
	spec.setStatus(http.StatusOK)
	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
	require.Empty(t, counterValues(t, reader, "manifold.openapi.spec_refresh.changes"))
}
