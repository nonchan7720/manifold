package cmd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestNewTokenVerifiers_OnlyServersWithAuthJWT(t *testing.T) {
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(jwks.Close)

	servers := config.Servers{
		"verified": &config.Server{Auth: &config.ServerAuth{JWT: &config.DownstreamJWT{
			Issuer: "https://issuer.example.com", JWKSURL: jwks.URL, Audience: "manifold",
		}}},
		"plain":   &config.Server{},
		"emptyau": &config.Server{Auth: &config.ServerAuth{}},
	}
	verifiers, err := newTokenVerifiers(t.Context(), servers)
	require.NoError(t, err)
	require.Len(t, verifiers, 1)
	require.Contains(t, verifiers, "verified")
}

func TestNewTokenVerifiers_UnreachableJWKSIsAStartupError(t *testing.T) {
	t.Setenv("TEST", "true")

	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()

	servers := config.Servers{
		"verified": &config.Server{Auth: &config.ServerAuth{JWT: &config.DownstreamJWT{
			Issuer: "https://issuer.example.com", JWKSURL: url, Audience: "manifold",
		}}},
	}
	_, err := newTokenVerifiers(t.Context(), servers)
	require.ErrorContains(t, err, "mcpServers.verified.auth.jwt")
}
