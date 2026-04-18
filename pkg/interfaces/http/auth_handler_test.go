package httphandler

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// --- generateRandomString ---

func TestGenerateRandomString_Length(t *testing.T) {
	for _, n := range []int{8, 16, 32, 43} {
		s := generateRandomString(n)
		require.Len(t, s, n, "expected length %d", n)
	}
}

func TestGenerateRandomString_Uniqueness(t *testing.T) {
	s1 := generateRandomString(32)
	s2 := generateRandomString(32)
	require.NotEqual(t, s1, s2, "two random strings should not be equal")
}

// --- generateS256Challenge ---

func TestGenerateS256Challenge(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	// RFC 7636 test vector
	challenge := generateS256Challenge(verifier)
	require.NotEmpty(t, challenge)
	// Base64URL エンコードのみ（padding なし）
	require.NotContains(t, challenge, "=")
}

func TestGenerateS256Challenge_Deterministic(t *testing.T) {
	verifier := "my-test-verifier-string"
	c1 := generateS256Challenge(verifier)
	c2 := generateS256Challenge(verifier)
	require.Equal(t, c1, c2, "same verifier should produce same challenge")
}

func TestGenerateS256Challenge_Different(t *testing.T) {
	c1 := generateS256Challenge("verifier-a")
	c2 := generateS256Challenge("verifier-b")
	require.NotEqual(t, c1, c2)
}

// --- getBaseURL ---

func TestGetBaseURL_HTTP(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/path", nil)
	req.Host = "example.com"
	got := h.getBaseURL(req)
	require.Equal(t, "http://example.com", got)
}

func TestGetBaseURL_XForwardedProto(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/path", nil)
	req.Host = "example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	got := h.getBaseURL(req)
	require.Equal(t, "https://example.com", got)
}

// --- OauthProtectedResource ---

func TestOauthProtectedResource(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "myserver",
		OAuth2: &config.OAuth2{Scopes: []string{"read", "write"}},
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-protected-resource/mcp/myserver", nil)
	req.Host = "gateway.example.com"
	ctx := contexts.ToServerContext(req.Context(), srv)
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()

	h.OauthProtectedResource(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, "application/json", rw.Header().Get("Content-Type"))

	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Contains(t, body, "resource")
	require.Contains(t, body, "authorization_servers")
	require.Contains(t, body, "scopes_supported")
}

func TestOauthProtectedResource_NoOAuth2(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "myserver",
		OAuth2: nil,
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-protected-resource/mcp/myserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.OauthProtectedResource(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	_, hasScopes := body["scopes_supported"]
	require.False(t, hasScopes)
}

// --- MetadataEndpoint ---

func TestMetadataEndpoint(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{Scopes: []string{"openid"}},
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)

	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "http://gateway.example.com/mcp/testserver", body["issuer"])
	require.Contains(t, body, "authorization_endpoint")
	require.Contains(t, body, "token_endpoint")
	require.Contains(t, body, "registration_endpoint")
	require.Contains(t, body, "code_challenge_methods_supported")
	require.Equal(t, []any{"S256"}, body["code_challenge_methods_supported"])
	require.Contains(t, body, "scopes_supported")
}

func TestMetadataEndpoint_NoOAuth2(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: nil,
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	_, hasScopes := body["scopes_supported"]
	require.False(t, hasScopes)
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

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	ctx := contexts.ToServerContext(req.Context(), srv)
	req = req.WithContext(ctx)
	rw := httptest.NewRecorder()

	handler.ServeHTTP(rw, req)

	require.Equal(t, srv, capturedSrv)
	require.Equal(t, http.StatusOK, rw.Code)
}

func TestWrapMCPServer_NoServerContext(t *testing.T) {
	var capturedSrv *config.Server
	inner := func(w http.ResponseWriter, r *http.Request, s *config.Server) {
		capturedSrv = s
		w.WriteHeader(http.StatusOK)
	}
	handler := wrapMCPServer(inner)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	require.Nil(t, capturedSrv)
}

// --- NewAuthHandler ---

func TestNewAuthHandler(t *testing.T) {
	servers := config.Servers{"test": &config.Server{}}
	h := NewAuthHandler(nil, servers)
	require.NotNil(t, h)
}

// --- RegisterClientEndpoint ---

func TestRegisterClientEndpoint_InvalidJSON(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader("invalid json"))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{
		Name: "test",
	})

	require.Equal(t, http.StatusBadRequest, rw.Code)
	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "invalid_client_metadata", body["error"])
}

func TestRegisterClientEndpoint_NoRedirectURIs(t *testing.T) {
	h := &AuthHandler{}
	reqBody := `{"grant_types": ["authorization_code"]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader(reqBody))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{
		Name: "test",
	})

	require.Equal(t, http.StatusBadRequest, rw.Code)
	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "invalid_redirect_uri", body["error"])
}

// --- LoginEndpoint ---

func TestLoginEndpoint_NilServer(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/auth/login", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, nil)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_NoOAuth2_NotMCPBackend(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: nil,
		Spec:   "local/spec.json", // OpenAPI mode, not MCP backend
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/testserver/auth/login", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_MissingPKCE(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{ClientID: "client1", AuthURL: "https://auth.example.com/auth", TokenURL: "https://auth.example.com/token"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/testserver/auth/login?client_id=client1", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- CallbackEndpoint ---

func TestCallbackEndpoint_MissingParams(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/testserver/auth/callback", nil)
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- TokenEndpoint ---

func TestTokenEndpoint_WrongGrantType(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/testserver/auth/token",
		strings.NewReader("grant_type=client_credentials"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestTokenEndpoint_MissingCodeOrVerifier(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/testserver/auth/token",
		strings.NewReader("grant_type=authorization_code"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- sendProbeRequest ---

func TestSendProbeRequest_Returns401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.Header().Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	headers, err := sendProbeRequest(context.Background(), srv.URL)
	require.NoError(t, err)
	require.NotEmpty(t, headers)
}

func TestSendProbeRequest_NonUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := sendProbeRequest(context.Background(), srv.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not return 401")
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
	require.Equal(t, "https://example.com/.well-known/oauth-protected-resource", url)
}

func TestGetResourceMetadata_NoResourceMetadata(t *testing.T) {
	header := `Bearer error="unauthorized"`
	_, err := getResourceMetadata([]string{header})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no resource_metadata")
}

// --- discoverOAuth2 cache ---

func TestDiscoverOAuth2_CacheHit(t *testing.T) {
	cachedOAuth2 := &config.OAuth2{
		ClientID: "cached-client",
		AuthURL:  "https://auth.example.com/auth",
		TokenURL: "https://auth.example.com/token",
	}
	servers := config.Servers{
		"myserver": &config.Server{
			Name:   "myserver",
			OAuth2: cachedOAuth2,
		},
	}
	h := &AuthHandler{servers: servers}

	srv := &config.Server{Name: "myserver"}
	result, err := h.discoverOAuth2(context.Background(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, cachedOAuth2, result)
}

// --- getAuthorizationServers ---

func TestGetAuthorizationServers_Success(t *testing.T) {
	// Protected Resource Metadata エンドポイントのモック
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              "http://example.com/resource",
			"authorization_servers": []string{"https://auth.example.com"},
		})
	}))
	defer srv.Close()

	servers, err := getAuthorizationServers(context.Background(), srv.URL, "http://example.com/resource", http.DefaultClient)
	require.NoError(t, err)
	require.Contains(t, servers, "https://auth.example.com")
}

func TestGetAuthorizationServers_Empty(t *testing.T) {
	// authorization_servers が空のレスポンス
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              "http://example.com/resource",
			"authorization_servers": []string{},
		})
	}))
	defer srv.Close()

	_, err := getAuthorizationServers(context.Background(), srv.URL, "http://example.com/resource", http.DefaultClient)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no authorization_servers")
}

// --- getAuthMetadata ---

func TestGetAuthMetadata_Success(t *testing.T) {
	// Authorization Server Metadata エンドポイントのモック
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		issuer := "http://" + r.Host
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"issuer":                           issuer,
			"authorization_endpoint":           issuer + "/auth",
			"token_endpoint":                   issuer + "/token",
			"response_types_supported":         []string{"code"},
			"grant_types_supported":            []string{"authorization_code"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer srv.Close()

	// mcpauth.GetAuthServerMetadata は .well-known/oauth-authorization-server を呼ぶ
	meta, err := getAuthMetadata(context.Background(), srv.URL, http.DefaultClient)
	require.NoError(t, err)
	require.NotNil(t, meta)
}

// --- mockStore ---

// mockStore はテスト用のインメモリ store.Client 実装。
// キーごとに返す値を事前に設定できる。
type mockStore struct {
	data map[string]string
}

func newMockStore(data map[string]string) *mockStore {
	return &mockStore{data: data}
}

func (m *mockStore) Set(_ context.Context, key string, value any, _ time.Duration) error {
	switch v := value.(type) {
	case string:
		m.data[key] = v
	case []byte:
		m.data[key] = string(v)
	default:
		m.data[key] = fmt.Sprintf("%v", v)
	}
	return nil
}

func (m *mockStore) Get(_ context.Context, key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("key not found: %s", key)
	}
	return v, nil
}

func (m *mockStore) Del(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockStore) Close() error { return nil }

var _ store.Client = (*mockStore)(nil)

// --- CallbackEndpoint: json.Unmarshal エラーハンドリング ---

func TestCallbackEndpoint_InvalidSessionJSON(t *testing.T) {
	st := newMockStore(map[string]string{
		"auth_session:validstate": "THIS IS NOT JSON",
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/testserver/auth/callback?state=validstate&code=somecode", nil)
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
}

// --- TokenEndpoint: json.Unmarshal エラーハンドリング ---

func TestTokenEndpoint_InvalidAuthCodeJSON(t *testing.T) {
	st := newMockStore(map[string]string{
		"auth_code:testcode": "THIS IS NOT JSON",
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{Name: "testserver"}

	body := "grant_type=authorization_code&code=testcode&code_verifier=verifier"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
}

func TestTokenEndpoint_InvalidUpstreamTokenJSON(t *testing.T) {
	// auth_code は正常（PKCE検証を通過できる値）
	verifier := generateRandomString(43)
	challenge := generateS256Challenge(verifier)
	authCodeData := AuthCodeData{
		CodeChallenge:    challenge,
		UpstreamTokenKey: "upstream_token:abc",
	}
	authCodeJSON, _ := json.Marshal(authCodeData)

	st := newMockStore(map[string]string{
		"auth_code:testcode": string(authCodeJSON),
		"upstream_token:abc": "THIS IS NOT JSON",
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{Name: "testserver"}

	body := fmt.Sprintf(
		"grant_type=authorization_code&code=testcode&code_verifier=%s", verifier,
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
}

// --- discoverOAuth2: DCR の client_secret が保存される ---

func TestDiscoverOAuth2_DCR_StoresClientSecret(t *testing.T) {
	// auth server: .well-known と DCR エンドポイントを提供
	var authServerURL string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		issuer := authServerURL
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"issuer":                           issuer,
				"authorization_endpoint":           issuer + "/auth",
				"token_endpoint":                   issuer + "/token",
				"registration_endpoint":            issuer + "/register",
				"response_types_supported":         []string{"code"},
				"grant_types_supported":            []string{"authorization_code"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"client_id":     "dcr-client-id",
				"client_secret": "dcr-client-secret",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authSrv.Close()
	authServerURL = authSrv.URL

	// Protected Resource Metadata エンドポイント
	var metaServerURL string
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              metaServerURL,
			"authorization_servers": []string{authServerURL},
		})
	}))
	defer metaSrv.Close()
	metaServerURL = metaSrv.URL

	// MCP バックエンド: 401 + resource_metadata を返す
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Www-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s"`, metaServerURL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	h := &AuthHandler{
		servers: config.Servers{
			"testsrv": &config.Server{Name: "testsrv"},
		},
	}
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       mcpSrv.URL,
	}

	result, err := h.discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "dcr-client-id", result.ClientID)
	require.Equal(t, "dcr-client-secret", result.ClientSecret)
	require.Equal(t, authServerURL+"/token", result.TokenURL)
	require.Equal(t, authServerURL+"/auth", result.AuthURL)
}

// --- validateRedirectURI ---

func TestValidateRedirectURI(t *testing.T) {
	tests := []struct {
		uri     string
		wantErr bool
	}{
		{"https://example.com/callback", false},
		{"https://app.example.com/cb", false},
		{"http://localhost/callback", false},
		{"http://localhost:3000/callback", false},
		{"http://127.0.0.1/callback", false},
		{"http://127.0.0.1:8080/callback", false},
		{"http://[::1]/callback", false},
		{"http://evil.com/callback", true},
		{"http://192.168.1.1/callback", true},
		{"ftp://example.com/callback", true},
		{"javascript:alert(1)", true},
		{"://invalid", true},
	}
	for _, tt := range tests {
		err := validateRedirectURI(tt.uri)
		if tt.wantErr {
			require.Error(t, err, "expected error for %q", tt.uri)
		} else {
			require.NoError(t, err, "unexpected error for %q", tt.uri)
		}
	}
}

// --- RegisterClientEndpoint: redirect_uri スキーム検証 ---

func TestRegisterClientEndpoint_InvalidRedirectURIScheme(t *testing.T) {
	h := &AuthHandler{}
	reqBody := `{"redirect_uris": ["http://evil.com/callback"]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader(reqBody))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{
		Name: "test",
	})

	require.Equal(t, http.StatusBadRequest, rw.Code)
	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	require.Equal(t, "invalid_redirect_uri", body["error"])
}

func TestRegisterClientEndpoint_LocalhostAllowed(t *testing.T) {
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{})
	reqBody := `{"redirect_uris": ["http://localhost:3000/callback"]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader(reqBody))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{
		Name: "test",
	})

	require.Equal(t, http.StatusCreated, rw.Code)
}

// --- LoginEndpoint: client_id / redirect_uri 検証 ---

func TestLoginEndpoint_UnknownClientID(t *testing.T) {
	st := newMockStore(map[string]string{}) // 登録済みクライアントなし
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{ClientID: "upstream", AuthURL: "https://auth.example.com/auth", TokenURL: "https://auth.example.com/token"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/testserver/auth/login?client_id=unknown&redirect_uri=https://example.com/cb&code_challenge=abc&code_challenge_method=S256", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestLoginEndpoint_MismatchedRedirectURI(t *testing.T) {
	clientReg := ClientRegistration{
		ClientID:     "client1",
		RedirectURIs: []string{"https://registered.example.com/callback"},
	}
	regJSON, _ := json.Marshal(clientReg)
	st := newMockStore(map[string]string{
		"oauth_client:client1": string(regJSON),
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{ClientID: "upstream", AuthURL: "https://auth.example.com/auth", TokenURL: "https://auth.example.com/token"},
	}
	// 登録されていない redirect_uri を指定
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/testserver/auth/login?client_id=client1&redirect_uri=https://evil.example.com/cb&code_challenge=abc&code_challenge_method=S256", nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_ValidClientAndRedirectURI(t *testing.T) {
	clientReg := ClientRegistration{
		ClientID:     "client1",
		RedirectURIs: []string{"https://app.example.com/callback"},
	}
	regJSON, _ := json.Marshal(clientReg)
	st := newMockStore(map[string]string{
		"oauth_client:client1": string(regJSON),
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{
		Name:   "testserver",
		OAuth2: &config.OAuth2{ClientID: "upstream", AuthURL: "https://auth.example.com/auth", TokenURL: "https://auth.example.com/token"},
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/testserver/auth/login?client_id=client1&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	// 上流 OAuth2 サーバーへリダイレクトされる
	require.Equal(t, http.StatusFound, rw.Code)
}

// --- TokenEndpoint: client_id 不一致 ---

func TestTokenEndpoint_ClientIDMismatch(t *testing.T) {
	verifier := generateRandomString(43)
	challenge := generateS256Challenge(verifier)
	authCodeData := AuthCodeData{
		ClientID:         "client1",
		CodeChallenge:    challenge,
		UpstreamTokenKey: "upstream_token:abc",
	}
	authCodeJSON, _ := json.Marshal(authCodeData)

	st := newMockStore(map[string]string{
		"auth_code:testcode": string(authCodeJSON),
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{Name: "testserver"}

	body := fmt.Sprintf(
		"grant_type=authorization_code&code=testcode&code_verifier=%s&client_id=wrong_client", verifier,
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

// --- encryptToken / decryptToken ---

func TestEncryptDecryptToken(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	plaintext := []byte(`{"access_token":"tok","token_type":"Bearer"}`)

	encrypted, err := h.encryptToken(plaintext)
	require.NoError(t, err)
	require.NotEmpty(t, encrypted)
	require.NotEqual(t, string(plaintext), encrypted)

	decrypted, err := h.decryptToken(encrypted)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestEncryptDecryptToken_TamperDetected(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	plaintext := []byte(`{"access_token":"tok"}`)

	encrypted, err := h.encryptToken(plaintext)
	require.NoError(t, err)

	// 改ざん: 末尾1バイト変更
	enc := []byte(encrypted)
	enc[len(enc)-1] ^= 0xFF
	_, err = h.decryptToken(string(enc))
	require.Error(t, err, "tampered ciphertext should fail decryption")
}

func TestIsPrivateIP(t *testing.T) {
	privates := []string{"127.0.0.1", "10.1.2.3", "192.168.0.1", "172.16.0.1", "169.254.1.1", "::1"}
	for _, s := range privates {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		require.True(t, isPrivateIP(ip), "expected private: %s", s)
	}

	publics := []string{"8.8.8.8", "1.1.1.1", "203.0.113.1"}
	for _, s := range publics {
		ip := net.ParseIP(s)
		require.NotNil(t, ip)
		require.False(t, isPrivateIP(ip), "expected public: %s", s)
	}
}

// --- LoginEndpoint: MCPServerName がセッションに保存される ---

func TestLoginEndpoint_SessionStoresMCPServerName(t *testing.T) {
	clientReg := ClientRegistration{
		ClientID:     "client1",
		RedirectURIs: []string{"https://app.example.com/callback"},
	}
	regJSON, _ := json.Marshal(clientReg)
	st := newMockStore(map[string]string{
		"oauth_client:client1": string(regJSON),
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{
		Name:   "myserver",
		OAuth2: &config.OAuth2{ClientID: "upstream", AuthURL: "https://auth.example.com/auth", TokenURL: "https://auth.example.com/token"},
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/myserver/auth/login?client_id=client1&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusFound, rw.Code)

	// 保存されたセッションに MCPServerName が設定されていることを確認
	var sessionKey string
	for k := range st.data {
		if len(k) > len("auth_session:") && k[:len("auth_session:")] == "auth_session:" {
			sessionKey = k
			break
		}
	}
	require.NotEmpty(t, sessionKey, "auth_session should be stored")

	var session AuthSession
	require.NoError(t, json.Unmarshal([]byte(st.data[sessionKey]), &session))
	require.Equal(t, "myserver", session.MCPServerName)
}

// --- CallbackEndpoint: MCPServerName が authCodeData に保存される ---

func TestCallbackEndpoint_AuthCodeStoresMCPServerName(t *testing.T) {
	// 上流トークンエンドポイントのモック
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"access_token": "upstream-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer tokenSrv.Close()

	session := AuthSession{
		ClientID:             "client1",
		RedirectURI:          "http://localhost:3000/callback",
		State:                "mystate",
		CodeChallenge:        "challenge",
		CodeChallengeMethod:  "S256",
		OAuth2ClientID:       "upstream-client",
		OAuth2ClientSecret:   "",
		OAuth2TokenURL:       tokenSrv.URL + "/token",
		UpstreamCodeVerifier: "verifier",
		MCPServerName:        "myserver",
	}
	sessionJSON, _ := json.Marshal(session)
	st := newMockStore(map[string]string{
		"auth_session:mystate": string(sessionJSON),
	})
	encKey := make([]byte, 32)
	h := NewAuthHandler(st, config.Servers{}, WithEncryptKey(encKey))
	h.httpClient = http.DefaultClient // テスト用にプライベートIP制限を無効化
	srv := &config.Server{
		Name: "myserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream-client",
			TokenURL: tokenSrv.URL + "/token",
		},
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/myserver/auth/callback?state=mystate&code=upstream-code", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	require.Equal(t, http.StatusFound, rw.Code)

	// 保存された authCode に MCPServerName が設定されていることを確認
	var authCodeKey string
	for k := range st.data {
		if len(k) > len("auth_code:") && k[:len("auth_code:")] == "auth_code:" {
			authCodeKey = k
			break
		}
	}
	require.NotEmpty(t, authCodeKey, "auth_code should be stored")

	var authCodeData AuthCodeData
	require.NoError(t, json.Unmarshal([]byte(st.data[authCodeKey]), &authCodeData))
	require.Equal(t, "myserver", authCodeData.MCPServerName)
}

// --- discoverOAuth2: DCR に refresh_token が含まれる ---

func TestDiscoverOAuth2_DCR_GrantTypesIncludeRefreshToken(t *testing.T) {
	var receivedGrantTypes []string

	var authServerURL string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		issuer := authServerURL
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"issuer":                           issuer,
				"authorization_endpoint":           issuer + "/auth",
				"token_endpoint":                   issuer + "/token",
				"registration_endpoint":            issuer + "/register",
				"response_types_supported":         []string{"code"},
				"grant_types_supported":            []string{"authorization_code", "refresh_token"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			var req struct {
				GrantTypes []string `json:"grant_types"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			receivedGrantTypes = req.GrantTypes
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"client_id":     "new-client-id",
				"client_secret": "",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer authSrv.Close()
	authServerURL = authSrv.URL

	var metaServerURL string
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              metaServerURL,
			"authorization_servers": []string{authServerURL},
		})
	}))
	defer metaSrv.Close()
	metaServerURL = metaSrv.URL

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Www-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s"`, metaServerURL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mcpSrv.Close()

	h := &AuthHandler{
		servers: config.Servers{
			"testsrv": &config.Server{Name: "testsrv"},
		},
	}
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       mcpSrv.URL,
	}

	_, err := h.discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)

	require.Contains(t, receivedGrantTypes, "authorization_code")
	require.Contains(t, receivedGrantTypes, "refresh_token")
}

// --- handleRefreshTokenGrant (TokenEndpoint 経由) ---

func TestTokenEndpoint_RefreshToken_MissingToken(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader("grant_type=refresh_token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestTokenEndpoint_RefreshToken_SessionNotFound(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader("grant_type=refresh_token&refresh_token=unknown"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestTokenEndpoint_RefreshToken_InvalidSessionData(t *testing.T) {
	st := newMockStore(map[string]string{
		"refresh_session:bad-token": "not-valid-encrypted-data",
	})
	h := NewAuthHandler(st, config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader("grant_type=refresh_token&refresh_token=bad-token"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestTokenEndpoint_RefreshToken_ClientIDMismatch(t *testing.T) {
	encKey := make([]byte, 32)
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{}, WithEncryptKey(encKey))

	rtSession := RefreshTokenSession{
		ClientID:       "registered-client",
		OAuth2TokenURL: "https://auth.example.com/token",
	}
	rtSessionJSON, _ := json.Marshal(rtSession)
	encrypted, err := h.encryptToken(rtSessionJSON)
	require.NoError(t, err)
	st.data["refresh_session:mytoken"] = encrypted

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token=mytoken&client_id=other-client"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, &config.Server{Name: "testserver"})

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestTokenEndpoint_RefreshToken_UpstreamFails(t *testing.T) {
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
	}))
	defer upstreamSrv.Close()

	encKey := make([]byte, 32)
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{}, WithEncryptKey(encKey))
	h.httpClient = http.DefaultClient

	rtSession := RefreshTokenSession{
		OAuth2ClientID: "upstream-client",
		OAuth2TokenURL: upstreamSrv.URL + "/token",
	}
	rtSessionJSON, _ := json.Marshal(rtSession)
	encrypted, err := h.encryptToken(rtSessionJSON)
	require.NoError(t, err)
	st.data["refresh_session:mytoken"] = encrypted

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token=mytoken"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, &config.Server{Name: "testserver"})

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestTokenEndpoint_RefreshToken_Success(t *testing.T) {
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.FormValue("grant_type"))
		require.Equal(t, "old-refresh-token", r.FormValue("refresh_token"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "new-refresh-token",
		})
	}))
	defer upstreamSrv.Close()

	encKey := make([]byte, 32)
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{}, WithEncryptKey(encKey))
	h.httpClient = http.DefaultClient

	rtSession := RefreshTokenSession{
		ClientID:       "client1",
		OAuth2ClientID: "upstream-client",
		OAuth2TokenURL: upstreamSrv.URL + "/token",
	}
	rtSessionJSON, _ := json.Marshal(rtSession)
	encrypted, err := h.encryptToken(rtSessionJSON)
	require.NoError(t, err)
	st.data["refresh_session:old-refresh-token"] = encrypted

	body := "grant_type=refresh_token&refresh_token=old-refresh-token&client_id=client1"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, &config.Server{Name: "testserver"})

	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, "application/json", rw.Header().Get("Content-Type"))

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &resp))
	require.Equal(t, "new-access-token", resp["access_token"])
	require.Equal(t, "new-refresh-token", resp["refresh_token"])

	// 古いセッションが削除され、新しいセッションが保存されていることを確認
	_, oldExists := st.data["refresh_session:old-refresh-token"]
	require.False(t, oldExists, "old refresh session should be deleted")
	_, newExists := st.data["refresh_session:new-refresh-token"]
	require.True(t, newExists, "new refresh session should be stored")
}

// --- CallbackEndpoint: 上流が refresh_token を返す場合 ---

func TestCallbackEndpoint_StoresRefreshSession(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "upstream-access-token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "upstream-refresh-token",
		})
	}))
	defer tokenSrv.Close()

	session := AuthSession{
		ClientID:             "client1",
		RedirectURI:          "http://localhost:3000/callback",
		State:                "mystate",
		CodeChallenge:        "challenge",
		CodeChallengeMethod:  "S256",
		OAuth2ClientID:       "upstream-client",
		OAuth2ClientSecret:   "upstream-secret",
		OAuth2TokenURL:       tokenSrv.URL + "/token",
		UpstreamCodeVerifier: "verifier",
		MCPServerName:        "myserver",
	}
	sessionJSON, _ := json.Marshal(session)
	st := newMockStore(map[string]string{
		"auth_session:mystate": string(sessionJSON),
	})
	encKey := make([]byte, 32)
	h := NewAuthHandler(st, config.Servers{}, WithEncryptKey(encKey))
	h.httpClient = http.DefaultClient
	srv := &config.Server{
		Name: "myserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream-client",
			TokenURL: tokenSrv.URL + "/token",
		},
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/myserver/auth/callback?state=mystate&code=upstream-code", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	require.Equal(t, http.StatusFound, rw.Code)

	// refresh_session が暗号化されて保存されていることを確認
	encryptedRTSession, exists := st.data["refresh_session:upstream-refresh-token"]
	require.True(t, exists, "refresh_session should be stored when upstream returns refresh_token")

	// 復号して内容を確認
	rtSessionJSON, err := h.decryptToken(encryptedRTSession)
	require.NoError(t, err)
	var rtSession RefreshTokenSession
	require.NoError(t, json.Unmarshal(rtSessionJSON, &rtSession))
	require.Equal(t, "upstream-client", rtSession.OAuth2ClientID)
	require.Equal(t, "upstream-secret", rtSession.OAuth2ClientSecret)
	require.Equal(t, tokenSrv.URL+"/token", rtSession.OAuth2TokenURL)
	require.Equal(t, "client1", rtSession.ClientID)
	require.Equal(t, "myserver", rtSession.MCPServerName)
}

// --- resourceURLFromMetaURL ---

func TestResourceURLFromMetaURL(t *testing.T) {
	tests := []struct {
		name    string
		metaURL string
		want    string
	}{
		{
			name:    "パスなし（ルートリソース）",
			metaURL: "https://example.com/.well-known/oauth-protected-resource",
			want:    "https://example.com",
		},
		{
			name:    "パスあり（/mcp）",
			metaURL: "https://mcp.notion.com/.well-known/oauth-protected-resource/mcp",
			want:    "https://mcp.notion.com/mcp",
		},
		{
			name:    "パスあり（/mcp/notion）",
			metaURL: "https://host/.well-known/oauth-protected-resource/mcp/notion",
			want:    "https://host/mcp/notion",
		},
		{
			name:    "HTTP スキーム",
			metaURL: "http://localhost:9999/.well-known/oauth-protected-resource/mcp/srv",
			want:    "http://localhost:9999/mcp/srv",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceURLFromMetaURL(tt.metaURL)
			require.Equal(t, tt.want, got)
		})
	}
}

// --- RegisterRoutes ---

func TestRegisterRoutes(t *testing.T) {
	mux := http.NewServeMux()
	h := NewAuthHandler(nil, config.Servers{})
	middleware := func(next http.HandlerFunc) http.HandlerFunc {
		return next
	}
	// RegisterRoutesがパニックしないことを確認
	require.NotPanics(t, func() {
		h.RegisterRoutes(mux, "server_name", middleware)
	})
}

// --- スパン記録テスト用ヘルパー ---

func setupTracerProvider(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	original := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(original)
		exporter.Reset()
	})
	return exporter
}

func spanByNameSuffix(spans tracetest.SpanStubs, suffix string) (tracetest.SpanStub, bool) {
	for _, s := range spans {
		if strings.HasSuffix(s.Name, suffix) {
			return s, true
		}
	}
	return tracetest.SpanStub{}, false
}

// --- RegisterClientEndpoint: スパンのエラー記録 ---

func TestRegisterClientEndpoint_InvalidJSON_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader("invalid json"))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{Name: "test"})

	require.Equal(t, http.StatusBadRequest, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/RegisterClientEndpoint")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

func TestRegisterClientEndpoint_InvalidRedirectURIScheme_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	h := &AuthHandler{}
	reqBody := `{"redirect_uris": ["http://evil.com/callback"]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader(reqBody))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{Name: "test"})

	require.Equal(t, http.StatusBadRequest, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/RegisterClientEndpoint")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

func TestRegisterClientEndpoint_Success_SpanNoError(t *testing.T) {
	exporter := setupTracerProvider(t)

	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{})
	reqBody := `{"redirect_uris": ["http://localhost:3000/callback"]}`
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/test/auth/clients", strings.NewReader(reqBody))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{Name: "test"})

	require.Equal(t, http.StatusCreated, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/RegisterClientEndpoint")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Unset, span.Status.Code)
}

// --- RegisterClientEndpointByClaudeCode: スパンのエラー記録 ---

func TestRegisterClientEndpointByClaudeCode_InvalidJSON_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/register", strings.NewReader("invalid json"))
	rw := httptest.NewRecorder()

	h.RegisterClientEndpointByClaudeCode(rw, req)

	require.Equal(t, http.StatusBadRequest, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/RegisterClientEndpointByClaudeCode")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

// --- CallbackEndpoint: スパンのエラー記録 ---

func TestCallbackEndpoint_InvalidSessionJSON_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	st := newMockStore(map[string]string{
		"auth_session:validstate": "THIS IS NOT JSON",
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/testserver/auth/callback?state=validstate&code=somecode", nil)
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/CallbackEndpoint")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

// --- TokenEndpoint: スパンのエラー記録 ---

func TestTokenEndpoint_InvalidAuthCodeJSON_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	st := newMockStore(map[string]string{
		"auth_code:testcode": "THIS IS NOT JSON",
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}
	srv := &config.Server{Name: "testserver"}

	body := "grant_type=authorization_code&code=testcode&code_verifier=verifier"
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/TokenEndpoint")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

// --- handleRefreshTokenGrant: スパンのエラー記録 ---

func TestTokenEndpoint_RefreshToken_SessionNotFound_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/testserver/auth/token", strings.NewReader("grant_type=refresh_token&refresh_token=unknown"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
	spans := exporter.GetSpans()
	// TokenEndpoint から handleRefreshTokenGrant が呼ばれ、そちらにエラーが記録される
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/handleRefreshTokenGrant")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

// --- sendProbeRequest: スパンのエラー記録 ---

func TestSendProbeRequest_NonUnauthorized_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := sendProbeRequest(context.Background(), srv.URL)
	require.Error(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/sendProbeRequest")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

func TestSendProbeRequest_Returns401_SpanNoError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := sendProbeRequest(context.Background(), srv.URL)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/sendProbeRequest")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Unset, span.Status.Code)
}

// --- getAuthorizationServers: スパンのエラー記録 ---

func TestGetAuthorizationServers_Empty_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              "http://example.com/resource",
			"authorization_servers": []string{},
		})
	}))
	defer srv.Close()

	_, err := getAuthorizationServers(context.Background(), srv.URL, "http://example.com/resource", http.DefaultClient)
	require.Error(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/getAuthorizationServers")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

func TestGetAuthorizationServers_Success_SpanNoError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              "http://example.com/resource",
			"authorization_servers": []string{"https://auth.example.com"},
		})
	}))
	defer srv.Close()

	_, err := getAuthorizationServers(context.Background(), srv.URL, "http://example.com/resource", http.DefaultClient)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/getAuthorizationServers")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Unset, span.Status.Code)
}

// --- getAuthMetadata: スパンのエラー記録 ---

func TestGetAuthMetadata_Failure_SpanRecordsError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := getAuthMetadata(context.Background(), srv.URL, http.DefaultClient)
	require.Error(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/getAuthMetadata")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

func TestGetAuthMetadata_Success_SpanNoError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"issuer":                           issuer,
			"authorization_endpoint":           issuer + "/auth",
			"token_endpoint":                   issuer + "/token",
			"response_types_supported":         []string{"code"},
			"grant_types_supported":            []string{"authorization_code"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	}))
	defer srv.Close()

	_, err := getAuthMetadata(context.Background(), srv.URL, http.DefaultClient)
	require.NoError(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/getAuthMetadata")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Unset, span.Status.Code)
}
