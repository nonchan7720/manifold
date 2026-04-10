package httphandler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- generateRandomString ---

func TestGenerateRandomString_Length(t *testing.T) {
	for _, n := range []int{8, 16, 32, 43} {
		s := generateRandomString(n)
		assert.Len(t, s, n, "expected length %d", n)
	}
}

func TestGenerateRandomString_Uniqueness(t *testing.T) {
	s1 := generateRandomString(32)
	s2 := generateRandomString(32)
	assert.NotEqual(t, s1, s2, "two random strings should not be equal")
}

// --- generateS256Challenge ---

func TestGenerateS256Challenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 test vector
	challenge := generateS256Challenge(verifier)
	assert.NotEmpty(t, challenge)
	// Base64URL エンコードのみ（padding なし）
	assert.NotContains(t, challenge, "=")
}

func TestGenerateS256Challenge_Deterministic(t *testing.T) {
	verifier := "my-test-verifier-string"
	c1 := generateS256Challenge(verifier)
	c2 := generateS256Challenge(verifier)
	assert.Equal(t, c1, c2, "same verifier should produce same challenge")
}

func TestGenerateS256Challenge_Different(t *testing.T) {
	c1 := generateS256Challenge("verifier-a")
	c2 := generateS256Challenge("verifier-b")
	assert.NotEqual(t, c1, c2)
}

// --- getBaseURL ---

func TestGetBaseURL_HTTP(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/path", nil)
	req.Host = "example.com"
	got := h.getBaseURL(req)
	assert.Equal(t, "http://example.com", got)
}

func TestGetBaseURL_XForwardedProto(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequest(http.MethodGet, "/path", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	got := h.getBaseURL(req)
	assert.Equal(t, "https://example.com", got)
}

// --- OauthProtectedResource ---

func TestOauthProtectedResource(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "myserver",
		OAuth2: &config.OAuth2{Scopes: []string{"read", "write"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp/myserver", nil)
	req.Host = "gateway.example.com"
	ctx := contexts.ToServerContext(req.Context(), srv)
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()

	h.OauthProtectedResource(rw, req, srv)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))

	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Contains(t, body, "resource")
	assert.Contains(t, body, "authorization_servers")
	assert.Contains(t, body, "scopes_supported")
}

func TestOauthProtectedResource_NoOAuth2(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "myserver",
		OAuth2: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp/myserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.OauthProtectedResource(rw, req, srv)

	assert.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	_, hasScopes := body["scopes_supported"]
	assert.False(t, hasScopes)
}

// --- MetadataEndpoint ---

func TestMetadataEndpoint(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{Scopes: []string{"openid"}},
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusOK, rw.Code)

	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Contains(t, body, "issuer")
	assert.Contains(t, body, "authorization_endpoint")
	assert.Contains(t, body, "token_endpoint")
	assert.Contains(t, body, "registration_endpoint")
	assert.Contains(t, body, "code_challenge_methods_supported")
	assert.Equal(t, []any{"S256"}, body["code_challenge_methods_supported"])
	assert.Contains(t, body, "scopes_supported")
}

func TestMetadataEndpoint_NoOAuth2(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: nil,
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	_, hasScopes := body["scopes_supported"]
	assert.False(t, hasScopes)
}

// --- wrapMCPServer ---

func TestWrapMCPServer_WithServerContext(t *testing.T) {
	srv := &config.Server{Name: "wrapped-server"}
	var capturedSrv *config.Server

	inner := func(w http.ResponseWriter, r *http.Request, s *config.Server) {
		capturedSrv = s
		w.WriteHeader(http.StatusOK)
	}
	handler := wrapMCPServer(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := contexts.ToServerContext(req.Context(), srv)
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()

	handler.ServeHTTP(rw, req)

	assert.Equal(t, srv, capturedSrv)
	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestWrapMCPServer_NoServerContext(t *testing.T) {
	var capturedSrv *config.Server
	inner := func(w http.ResponseWriter, r *http.Request, s *config.Server) {
		capturedSrv = s
		w.WriteHeader(http.StatusOK)
	}
	handler := wrapMCPServer(inner)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.Nil(t, capturedSrv)
}

// --- NewAuthHandler ---

func TestNewAuthHandler(t *testing.T) {
	servers := config.Servers{"test": config.Server{}}
	h := NewAuthHandler(nil, servers)
	require.NotNil(t, h)
}

// --- RegisterClientEndpoint ---

func TestRegisterClientEndpoint_InvalidJSON(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequest(http.MethodPost, "/test/auth/clients", strings.NewReader("invalid json"))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_client_metadata", body["error"])
}

func TestRegisterClientEndpoint_NoRedirectURIs(t *testing.T) {
	h := &AuthHandler{}
	reqBody := `{"grant_types": ["authorization_code"]}`
	req := httptest.NewRequest(http.MethodPost, "/test/auth/clients", strings.NewReader(reqBody))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "invalid_redirect_uri", body["error"])
}

// --- LoginEndpoint ---

func TestLoginEndpoint_NilServer(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequest(http.MethodGet, "/test/auth/login", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, nil)

	assert.Equal(t, http.StatusNotFound, rw.Code)
}

func TestLoginEndpoint_NoOAuth2_NotMCPBackend(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: nil,
		Spec:   "local/spec.json", // OpenAPI mode, not MCP backend
	}
	req := httptest.NewRequest(http.MethodGet, "/testserver/auth/login", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
}

func TestLoginEndpoint_MissingPKCE(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{ClientID: "client1", AuthURL: "https://auth.example.com/auth", TokenURL: "https://auth.example.com/token"},
	}
	req := httptest.NewRequest(http.MethodGet, "/testserver/auth/login?client_id=client1", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- CallbackEndpoint ---

func TestCallbackEndpoint_MissingParams(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequest(http.MethodGet, "/testserver/auth/callback", nil)
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- TokenEndpoint ---

func TestTokenEndpoint_WrongGrantType(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequest(http.MethodPost, "/testserver/auth/token",
		strings.NewReader("grant_type=client_credentials"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestTokenEndpoint_MissingCodeOrVerifier(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequest(http.MethodPost, "/testserver/auth/token",
		strings.NewReader("grant_type=authorization_code"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	assert.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- sendProbeRequest ---

func TestSendProbeRequest_Returns401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	headers, err := sendProbeRequest(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.NotEmpty(t, headers)
}

func TestSendProbeRequest_NonUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := sendProbeRequest(context.Background(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not return 401")
}

func TestSendProbeRequest_InvalidURL(t *testing.T) {
	_, err := sendProbeRequest(context.Background(), "://invalid")
	require.Error(t, err)
}

// --- getResourceMetadata ---

func TestGetResourceMetadata_EmptyHeaders(t *testing.T) {
	_, err := getResourceMetadata([]string{})
	require.Error(t, err)
}

func TestGetResourceMetadata_ValidHeader(t *testing.T) {
	header := `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`
	url, err := getResourceMetadata([]string{header})
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/.well-known/oauth-protected-resource", url)
}

func TestGetResourceMetadata_NoResourceMetadata(t *testing.T) {
	header := `Bearer error="unauthorized"`
	_, err := getResourceMetadata([]string{header})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no resource_metadata")
}

// --- discoverOAuth2 cache ---

func TestDiscoverOAuth2_CacheHit(t *testing.T) {
	cachedOAuth2 := &config.OAuth2{
		ClientID: "cached-client",
		AuthURL:  "https://auth.example.com/auth",
		TokenURL: "https://auth.example.com/token",
	}
	servers := config.Servers{
		"myserver": config.Server{
			Name:   "myserver",
			OAuth2: cachedOAuth2,
		},
	}
	h := &AuthHandler{servers: servers}

	srv := &config.Server{Name: "myserver"}
	result, err := h.discoverOAuth2(context.Background(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	assert.Equal(t, cachedOAuth2, result)
}

// --- getAuthorizationServers ---

func TestGetAuthorizationServers_Success(t *testing.T) {
	// Protected Resource Metadata エンドポイントのモック
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":             "http://example.com/resource",
			"authorization_servers": []string{"https://auth.example.com"},
		})
	}))
	defer srv.Close()

	servers, err := getAuthorizationServers(context.Background(), srv.URL, "http://example.com/resource")
	require.NoError(t, err)
	assert.Contains(t, servers, "https://auth.example.com")
}

func TestGetAuthorizationServers_Empty(t *testing.T) {
	// authorization_servers が空のレスポンス
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":             "http://example.com/resource",
			"authorization_servers": []string{},
		})
	}))
	defer srv.Close()

	_, err := getAuthorizationServers(context.Background(), srv.URL, "http://example.com/resource")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no authorization_servers")
}

// --- getAuthMetadata ---

func TestGetAuthMetadata_Success(t *testing.T) {
	// Authorization Server Metadata エンドポイントのモック
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		issuer := "http://" + r.Host
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"issuer":                            issuer,
			"authorization_endpoint":            issuer + "/auth",
			"token_endpoint":                    issuer + "/token",
			"response_types_supported":          []string{"code"},
			"grant_types_supported":             []string{"authorization_code"},
			"code_challenge_methods_supported":  []string{"S256"},
		})
	}))
	defer srv.Close()

	// mcpauth.GetAuthServerMetadata は .well-known/oauth-authorization-server を呼ぶ
	meta, err := getAuthMetadata(context.Background(), srv.URL)
	require.NoError(t, err)
	assert.NotNil(t, meta)
}

// --- RegisterRoutes ---

func TestRegisterRoutes(t *testing.T) {
	mux := http.NewServeMux()
	h := NewAuthHandler(nil, config.Servers{})
	middleware := func(next http.HandlerFunc) http.HandlerFunc {
		return next
	}
	// RegisterRoutesがパニックしないことを確認
	assert.NotPanics(t, func() {
		h.RegisterRoutes(mux, "server_name", middleware)
	})
}
