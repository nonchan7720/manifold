package mcpsrv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// --- contextOAuthHandler ---

func TestContextOAuthHandler_TokenSource_WithToken(t *testing.T) {
	h := &contextOAuthHandler{}
	ctx := contexts.ToRequestAuthHeader(context.Background(), "Bearer my-access-token")

	ts, err := h.TokenSource(ctx)
	require.NoError(t, err)
	require.NotNil(t, ts)

	token, err := ts.Token()
	require.NoError(t, err)
	require.Equal(t, "my-access-token", token.AccessToken)
}

func TestContextOAuthHandler_TokenSource_NoToken(t *testing.T) {
	h := &contextOAuthHandler{}
	ts, err := h.TokenSource(context.Background())
	require.NoError(t, err)
	require.Nil(t, ts)
}

func TestContextOAuthHandler_TokenSource_BearerPrefix(t *testing.T) {
	h := &contextOAuthHandler{}
	ctx := contexts.ToRequestAuthHeader(context.Background(), "Bearer token-value")

	ts, err := h.TokenSource(ctx)
	require.NoError(t, err)
	require.NotNil(t, ts)

	token, err := ts.Token()
	require.NoError(t, err)
	// "Bearer " プレフィックスが取り除かれる
	require.Equal(t, "token-value", token.AccessToken)
}

func TestContextOAuthHandler_Authorize(t *testing.T) {
	h := &contextOAuthHandler{}
	resp := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       http.NoBody,
	}
	err := h.Authorize(context.Background(), nil, resp)
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
}

// --- extraHeaderRoundTripper ---

func TestExtraHeaderRoundTripper_WithHeaders(t *testing.T) {
	var capturedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("X-Custom-Header")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &extraHeaderRoundTripper{
		headers: map[string]string{
			"X-Custom-Header": "custom-value",
		},
		base: http.DefaultTransport,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "custom-value", capturedHeader)
}

func TestExtraHeaderRoundTripper_NoHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &extraHeaderRoundTripper{
		headers: nil,
		base:    http.DefaultTransport,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestExtraHeaderRoundTripper_EmptyHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &extraHeaderRoundTripper{
		headers: map[string]string{},
		base:    http.DefaultTransport,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// --- authValueRoundTripper ---

func TestAuthValueRoundTripper_WithPrefix(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &authValueRoundTripper{
		authValue: &config.AuthValue{
			Header: "Authorization",
			Prefix: "Bearer",
			Value:  "mytoken",
		},
		base: http.DefaultTransport,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "Bearer mytoken", capturedAuth)
}

func TestAuthValueRoundTripper_NoPrefix(t *testing.T) {
	var capturedHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeader = r.Header.Get("X-Api-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &authValueRoundTripper{
		authValue: &config.AuthValue{
			Header: "X-Api-Key",
			Prefix: "",
			Value:  "secret-key",
		},
		base: http.DefaultTransport,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "secret-key", capturedHeader)
}

// --- buildTransport ---

func TestBuildTransport_StdioEmptyCommand(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportStdio,
			Command:   "", // 空のコマンド
		},
	}

	_, err := c.buildTransport(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "command is required")
}

func TestBuildTransport_UnknownTransport(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: "unknown",
		},
	}

	_, err := c.buildTransport(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown transport")
}

func TestBuildTransport_HTTP_NoAuthValue(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

func TestBuildTransport_HTTP_WithAuthValue(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportHTTP,
			URL:       "http://backend.example.com/mcp",
			AuthValue: &config.AuthValue{
				Header: "Authorization",
				Prefix: "Bearer",
				Value:  "static-token",
			},
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

func TestBuildTransport_Stdio_WithCommand(t *testing.T) {
	c := &MCPBackendClient{
		name: "testbackend",
		cfg: &config.Server{
			Transport: config.MCPTransportStdio,
			Command:   "/bin/echo",
			Args:      []string{"hello"},
			Env:       map[string]string{"TEST_VAR": "value"},
		},
	}

	transport, err := c.buildTransport(context.Background())
	require.NoError(t, err)
	require.NotNil(t, transport)
}

// --- MCPBackendClient.Close ---

func TestMCPBackendClient_Close_NotConnected(t *testing.T) {
	c := &MCPBackendClient{
		name:      "test",
		cfg:       &config.Server{},
		connected: false,
		session:   nil,
	}
	// 接続していない場合はパニックしない
	require.NotPanics(t, func() {
		c.Close()
	})
	require.False(t, c.connected)
}
