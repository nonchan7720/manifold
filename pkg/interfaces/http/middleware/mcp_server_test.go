package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPServerApp_ServerNotFound(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{BaseURL: "http://example.com"},
	}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	m := MCPServerApp(servers, "server_name")

	mux := http.NewServeMux()
	mux.HandleFunc("/{server_name}/mcp", m(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/unknown/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusNotFound, rw.Code)
	assert.False(t, called)
}

func TestMCPServerApp_ServerFound(t *testing.T) {
	servers := config.Servers{
		"myserver": &config.Server{Name: "myserver", BaseURL: "http://example.com"},
	}
	var gotServer *config.Server
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotServer = contexts.FromServerContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	m := MCPServerApp(servers, "server_name")

	mux := http.NewServeMux()
	mux.HandleFunc("/{server_name}/mcp", m(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/myserver/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	require.NotNil(t, gotServer)
	assert.Equal(t, "myserver", gotServer.Name)
	assert.Equal(t, "http://example.com", gotServer.BaseURL)
}

func TestMCPServerApp_HeaderExtraction(t *testing.T) {
	servers := config.Servers{
		"svc": &config.Server{Name: "svc", BaseURL: "http://backend.example.com"},
	}
	var gotHeaders map[string][]string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = contexts.FromHeaderContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	m := MCPServerApp(servers, "server_name")

	mux := http.NewServeMux()
	mux.HandleFunc("/{server_name}/mcp", m(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/svc/mcp", nil)
	// プレフィックス "x-svc-" を持つヘッダーを小文字キーで直接設定（HTTP/2スタイル）
	req.Header["x-svc-tenant-id"] = []string{"tenant-abc"}
	req.Header["x-svc-region"] = []string{"us-east-1"}
	req.Header["X-Other-Header"] = []string{"other-value"}
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.NotNil(t, gotHeaders)
	// "x-svc-tenant-id" → プレフィックス "x-svc-" を除いた "tenant-id" がキー
	assert.Equal(t, []string{"tenant-abc"}, gotHeaders["tenant-id"])
	assert.Equal(t, []string{"us-east-1"}, gotHeaders["region"])
	// 関係ないヘッダーは含まれない
	_, hasOther := gotHeaders["X-Other-Header"]
	assert.False(t, hasOther)
}
