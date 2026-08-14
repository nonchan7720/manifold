package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

func TestOAuth2RoundTripper_NoToken(t *testing.T) {
	rt := NewOAuth2RoundTripper(http.DefaultTransport)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"http://example.com",
		nil,
	)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestOAuth2RoundTripper_WithToken(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := NewOAuth2RoundTripper(http.DefaultTransport)

	// contexts.RequestAuthHeader にはプレフィックスなしの生トークンが保存される想定
	ctx := contexts.ToRequestAuthHeader(context.Background(), "my-token")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	require.Equal(t, "Bearer my-token", capturedAuth)
}

func TestOAuth2RoundTripper_TokenAlreadyHasBearerPrefix(t *testing.T) {
	var capturedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := NewOAuth2RoundTripper(http.DefaultTransport)

	// "Bearer " が既に付いていても二重にならない
	ctx := contexts.ToRequestAuthHeader(context.Background(), "Bearer my-token")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	require.Equal(t, "Bearer my-token", capturedAuth)
}
