package middleware

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/services/identity"
	"github.com/stretchr/testify/require"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{"valid bearer", "Bearer token123", "token123"},
		{"empty header", "", ""},
		{"basic auth", "Basic dXNlcjpwYXNz", ""},
		{"no space after Bearer", "Bearertoken", ""},
		{
			"bearer with complex token",
			"Bearer eyJhbGciOiJSUzI1NiJ9.payload.sig",
			"eyJhbGciOiJSUzI1NiJ9.payload.sig",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got := extractBearerToken(r)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestJWT_ServerNotFound(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{},
	}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name", nil)(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/unknown/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	require.True(t, called)
	require.Equal(t, http.StatusOK, rw.Code)
}

func TestJWT_NoOAuth2_WithToken(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{OAuth2: nil},
	}
	var capturedToken string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = contexts.FromRequestAuthHeader(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name", nil)(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	req.Header.Set("Authorization", "Bearer my-token")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, "my-token", capturedToken)
}

func TestJWT_NoOAuth2_NoToken(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{OAuth2: nil},
	}
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name", nil)(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	require.False(t, called)
	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestJWT_WithOAuth2_NoToken(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name", nil)(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.Contains(t, rw.Header().Get("WWW-Authenticate"), "Bearer resource_metadata=")
}

func TestJWT_WithOAuth2_WithToken(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	var capturedToken string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedToken = contexts.FromRequestAuthHeader(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name", nil)(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	req.Header.Set("Authorization", "Bearer oauth-token")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, "oauth-token", capturedToken)
}

func TestJWT_WithOAuth2_XForwardedProto(t *testing.T) {
	servers := config.Servers{
		"test": &config.Server{OAuth2: &config.OAuth2{ClientID: "client1"}},
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/{server_name}/mcp", JWT(servers, "server_name", nil)(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/mcp", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	require.Equal(t, http.StatusUnauthorized, rw.Code)
	wwwAuth := rw.Header().Get("WWW-Authenticate")
	require.Contains(t, wwwAuth, "https://")
}

// --- auth.jwt（Manifold 側での JWT 検証） ---

type jwtTestEnv struct {
	priv    *rsa.PrivateKey
	issuer  string
	handler http.Handler
	called  *bool
}

func newJWTTestEnv(t *testing.T, verified bool) *jwtTestEnv {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwk, err := jwkset.NewJWKFromKey(&priv.PublicKey, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: "kid", ALG: jwkset.AlgRS256, USE: jwkset.UseSig},
	})
	require.NoError(t, err)
	body, err := json.Marshal(jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwk.Marshal()}})
	require.NoError(t, err)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(jwks.Close)

	const issuer = "https://issuer.example.com"
	var verifiers map[string]TokenVerifier
	if verified {
		v, err := identity.NewJWTVerifier(t.Context(), "ga4", &config.DownstreamJWT{
			Issuer: issuer, JWKSURL: jwks.URL, Audience: "manifold",
		})
		require.NoError(t, err)
		verifiers = map[string]TokenVerifier{"ga4": v}
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		require.NotEmpty(t, contexts.FromRequestAuthHeader(r.Context()))
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle(
		"/{server_name}/mcp",
		JWT(config.Servers{"ga4": &config.Server{}}, "server_name", verifiers)(next),
	)
	return &jwtTestEnv{priv: priv, issuer: issuer, handler: mux, called: &called}
}

func (e *jwtTestEnv) token(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss": e.issuer, "sub": "u", "aud": "manifold", "exp": time.Now().Add(time.Hour).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "kid"
	s, err := tok.SignedString(e.priv)
	require.NoError(t, err)
	return s
}

func (e *jwtTestEnv) do(t *testing.T, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ga4/mcp", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rw := httptest.NewRecorder()
	e.handler.ServeHTTP(rw, req)
	return rw
}

func TestJWT_Verifier_ValidToken_Passes(t *testing.T) {
	e := newJWTTestEnv(t, true)
	rw := e.do(t, e.token(t, nil))
	require.Equal(t, http.StatusOK, rw.Code)
	require.True(t, *e.called)
}

func TestJWT_Verifier_RejectsWith401AndChallenge(t *testing.T) {
	e := newJWTTestEnv(t, true)
	tests := map[string]string{
		"missing": "",
		"opaque":  "opaque-token",
		"expired": e.token(
			t,
			func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		),
		"wrong aud":    e.token(t, func(c jwt.MapClaims) { c["aud"] = "other" }),
		"wrong issuer": e.token(t, func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" }),
	}
	for name, bearer := range tests {
		t.Run(name, func(t *testing.T) {
			*e.called = false
			rw := e.do(t, bearer)
			require.Equal(t, http.StatusUnauthorized, rw.Code)
			require.Equal(
				t,
				`Bearer resource_metadata="http://example.com/.well-known/oauth-protected-resource"`,
				rw.Header().Get("WWW-Authenticate"),
			)
			require.False(t, *e.called)
		})
	}
}

func TestJWT_NoVerifier_OpaqueTokenStillPassesThrough(t *testing.T) {
	e := newJWTTestEnv(t, false)
	rw := e.do(t, "opaque-token")
	require.Equal(t, http.StatusOK, rw.Code)
	require.True(t, *e.called)
}
