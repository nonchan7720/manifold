package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/assert"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{"valid bearer", "Bearer token123", "Bearer token123"},
		{"empty header", "", ""},
		{"basic auth", "Basic dXNlcjpwYXNz", ""},
		{"no space after Bearer", "Bearertoken", ""},
		{"bearer with complex token", "Bearer eyJhbGciOiJSUzI1NiJ9.payload.sig", "Bearer eyJhbGciOiJSUzI1NiJ9.payload.sig"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got := extractBearerToken(r)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestJWT_ServerNotFound(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{},
	}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name")(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/unknown/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestJWT_NoOAuth2_WithToken(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{OAuth2: nil},
	}
	var capturedToken string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = contexts.FromRequestAuthHeader(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name")(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "Bearer my-token", capturedToken)
}

func TestJWT_NoOAuth2_NoToken(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{OAuth2: nil},
	}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name")(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestJWT_WithOAuth2_NoToken(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name")(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	assert.Contains(t, rw.Header().Get("WWW-Authenticate"), "Bearer resource_metadata=")
}

func TestJWT_WithOAuth2_WithToken(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	var capturedToken string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = contexts.FromRequestAuthHeader(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name")(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	req.Header.Set("Authorization", "Bearer oauth-token")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "Bearer oauth-token", capturedToken)
}

func TestJWT_WithOAuth2_XForwardedProto(t *testing.T) {
	servers := config.Servers{
		"test": config.Server{OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name")(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	assert.Equal(t, http.StatusUnauthorized, rw.Code)
	wwwAuth := rw.Header().Get("WWW-Authenticate")
	assert.Contains(t, wwwAuth, "https://")
}
