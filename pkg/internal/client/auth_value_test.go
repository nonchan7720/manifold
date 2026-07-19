package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

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

func TestAuthValueRoundTripper_NilAuthValue_PassesThrough(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// authValue が nil の場合は何もヘッダーを付加せずそのまま転送する
	// （extraHeaderRoundTripper の nil-safety と同じ扱い）。
	rt := &authValueRoundTripper{
		authValue: nil,
		base:      http.DefaultTransport,
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "existing")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "existing", capturedAuth)
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
