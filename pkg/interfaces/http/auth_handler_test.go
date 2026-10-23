package httphandler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/util"
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"http://example.com/path",
		nil,
	)
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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/.well-known/oauth-protected-resource/mcp/myserver",
		nil,
	)
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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/.well-known/oauth-protected-resource/mcp/myserver",
		nil,
	)
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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/.well-known/oauth-authorization-server/mcp/testserver",
		nil,
	)
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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/.well-known/oauth-authorization-server/mcp/testserver",
		nil,
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader("invalid json"),
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader(reqBody),
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/login",
		nil,
	)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_MissingPKCE(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "client1",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/login?client_id=client1",
		nil,
	)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- CallbackEndpoint ---

func TestCallbackEndpoint_MissingParams(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/callback",
		nil,
	)
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
		w.Header().
			Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	headers, err := sendProbeRequest(context.Background(), srv.URL, nil)
	require.NoError(t, err)
	require.NotEmpty(t, headers)
}

func TestSendProbeRequest_NonUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := sendProbeRequest(context.Background(), srv.URL, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "did not return 401")
}

func TestSendProbeRequest_InvalidURL(t *testing.T) {
	_, err := sendProbeRequest(context.Background(), "://invalid", nil)
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

	servers, err := getAuthorizationServers(
		context.Background(),
		srv.URL,
		"http://example.com/resource",
		http.DefaultClient,
	)
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

	_, err := getAuthorizationServers(
		context.Background(),
		srv.URL,
		"http://example.com/resource",
		http.DefaultClient,
	)
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

// Get はキー不存在を store.ErrNotFound をラップして返す。実装（memory / sqlite）と
// 同じ契約にしておかないと、呼び出し側の errors.Is による分岐をテストで検証できない。
func (m *mockStore) Get(_ context.Context, key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("key not found: %s: %w", key, store.ErrNotFound)
	}
	return v, nil
}

func (m *mockStore) Expire(_ context.Context, key string, _ time.Duration) error {
	if _, ok := m.data[key]; !ok {
		return fmt.Errorf("key not found: %s", key)
	}
	return nil
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
	var metaServerURL, mcpServerURL string
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			// RFC 9728 §3.3: resource はリクエスト先 URL (MCP バックエンド) と一致する
			"resource":              mcpServerURL,
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
	mcpServerURL = mcpSrv.URL

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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader(reqBody),
	)
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

func TestRegisterClientEndpoint_StoresDCRSource(t *testing.T) {
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{})
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader(`{"redirect_uris": ["https://app.example.com/callback"]}`),
	)
	rw := httptest.NewRecorder()

	h.RegisterClientEndpoint(rw, req, &config.Server{Name: "test"})
	require.Equal(t, http.StatusCreated, rw.Code)

	var reg ClientRegistration
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &reg))
	stored, ok := st.data[dcrClientKeyPrefix+reg.ClientID]
	require.True(t, ok)
	var storeReg StoreClientRegistration
	require.NoError(t, json.Unmarshal([]byte(stored), &storeReg))
	require.Equal(t, ClientSourceDCR, storeReg.Source)
	require.Equal(t, "test", storeReg.MCPServerName)
}

func TestRegisterClientEndpoint_LocalhostAllowed(t *testing.T) {
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{})
	reqBody := `{"redirect_uris": ["http://localhost:3000/callback"]}`
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader(reqBody),
	)
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
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/login?client_id=unknown&redirect_uri=https://example.com/cb&code_challenge=abc&code_challenge_method=S256",
		nil,
	)
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
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}
	// 登録されていない redirect_uri を指定
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/login?client_id=client1&redirect_uri=https://evil.example.com/cb&code_challenge=abc&code_challenge_method=S256",
		nil,
	)
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
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/login?client_id=client1&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st",
		nil,
	)
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
		"grant_type=authorization_code&code=testcode&code_verifier=%s&client_id=wrong_client",
		verifier,
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

// --- resolveUpstreamClient ---

func TestResolveUpstreamClient(t *testing.T) {
	mapped := []config.OAuth2Client{{
		DownstreamClientID: "client1",
		ClientID:           "mapped-client",
		ClientSecret:       "mapped-secret",
	}}
	tests := []struct {
		name       string
		oauth2     *config.OAuth2
		downstream string
		wantID     string
		wantSecret string
		wantErr    bool
	}{
		{
			name:       "マッピング済み",
			oauth2:     &config.OAuth2{ClientID: "shared", Clients: mapped},
			downstream: "client1",
			wantID:     "mapped-client",
			wantSecret: "mapped-secret",
		},
		{
			name:       "未登録は既定で拒否",
			oauth2:     &config.OAuth2{ClientID: "shared", Clients: mapped},
			downstream: "other",
			wantErr:    true,
		},
		{
			name: "未登録でも default なら共用クライアント",
			oauth2: &config.OAuth2{
				ClientID:      "shared",
				ClientSecret:  "shared-secret",
				UnknownClient: config.OAuth2UnknownClientDefault,
				Clients:       mapped,
			},
			downstream: "other",
			wantID:     "shared",
			wantSecret: "shared-secret",
		},
		{
			name:       "clients 未設定なら共用クライアント",
			oauth2:     &config.OAuth2{ClientID: "shared", ClientSecret: "shared-secret"},
			downstream: "client1",
			wantID:     "shared",
			wantSecret: "shared-secret",
		},
		{
			name: "clients 未設定でも明示的に拒否できる",
			oauth2: &config.OAuth2{
				ClientID:      "shared",
				UnknownClient: config.OAuth2UnknownClientReject,
			},
			downstream: "client1",
			wantErr:    true,
		},
		{
			name:       "oauth2 未設定",
			oauth2:     nil,
			downstream: "client1",
			wantErr:    true,
		},
	}
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &config.Server{Name: "myserver", OAuth2: tt.oauth2}
			gotID, gotSecret, err := h.resolveUpstreamClient(srv, tt.downstream)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantID, gotID)
			require.Equal(t, tt.wantSecret, gotSecret)
		})
	}
}

// --- LoginEndpoint: 下流クライアント → 上流クライアントのマッピング ---

// newMappedLoginHandler は client1 を DCR 済みクライアントとして持つハンドラを返す。
func newMappedLoginHandler(t *testing.T) (*AuthHandler, *mockStore) {
	t.Helper()
	regJSON, err := json.Marshal(ClientRegistration{
		ClientID:     "client1",
		RedirectURIs: []string{"https://app.example.com/callback"},
	})
	require.NoError(t, err)
	st := newMockStore(map[string]string{"oauth_client:client1": string(regJSON)})
	return NewAuthHandler(st, config.Servers{}), st
}

func mappedLoginServer(oauth2 *config.OAuth2) *config.Server {
	return &config.Server{Name: "myserver", OAuth2: oauth2}
}

func mappedLoginRequest(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/myserver/auth/login?client_id=client1"+
			"&redirect_uri=https://app.example.com/callback"+
			"&code_challenge=abc&code_challenge_method=S256&state=st", nil)
	req.Host = "gateway.example.com"
	return req
}

// upstreamAuthQuery は Location ヘッダーの上流認可 URL のクエリを返す。
func upstreamAuthQuery(t *testing.T, rw *httptest.ResponseRecorder) url.Values {
	t.Helper()
	loc, err := url.Parse(rw.Header().Get("Location"))
	require.NoError(t, err)
	return loc.Query()
}

// storedAuthSession は store に保存された唯一の認証セッションを返す。
func storedAuthSession(t *testing.T, st *mockStore) AuthSession {
	t.Helper()
	for k, v := range st.data {
		if !strings.HasPrefix(k, "auth_session:") {
			continue
		}
		var session AuthSession
		require.NoError(t, json.Unmarshal([]byte(v), &session))
		return session
	}
	t.Fatal("auth_session should be stored")
	return AuthSession{}
}

func TestLoginEndpoint_MappedUpstreamClient(t *testing.T) {
	h, st := newMappedLoginHandler(t)
	srv := mappedLoginServer(&config.OAuth2{
		ClientID:     "shared",
		ClientSecret: "shared-secret",
		AuthURL:      "https://auth.example.com/auth",
		TokenURL:     "https://auth.example.com/token",
		Clients: []config.OAuth2Client{{
			DownstreamClientID: "client1",
			ClientID:           "mapped-client",
			ClientSecret:       "mapped-secret",
		}},
	})
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, mappedLoginRequest(t), srv)

	require.Equal(t, http.StatusFound, rw.Code)
	require.Equal(t, "mapped-client", upstreamAuthQuery(t, rw).Get("client_id"))

	session := storedAuthSession(t, st)
	require.Equal(t, "mapped-client", session.OAuth2ClientID)
	require.Equal(t, "mapped-secret", session.OAuth2ClientSecret)
}

func TestLoginEndpoint_UnmappedUpstreamClientRejected(t *testing.T) {
	h, _ := newMappedLoginHandler(t)
	srv := mappedLoginServer(&config.OAuth2{
		ClientID:     "shared",
		ClientSecret: "shared-secret",
		AuthURL:      "https://auth.example.com/auth",
		TokenURL:     "https://auth.example.com/token",
		Clients: []config.OAuth2Client{{
			DownstreamClientID: "other",
			ClientID:           "mapped-client",
		}},
	})
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, mappedLoginRequest(t), srv)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestLoginEndpoint_UnmappedUpstreamClientDefault(t *testing.T) {
	h, _ := newMappedLoginHandler(t)
	srv := mappedLoginServer(&config.OAuth2{
		ClientID:      "shared",
		ClientSecret:  "shared-secret",
		AuthURL:       "https://auth.example.com/auth",
		TokenURL:      "https://auth.example.com/token",
		UnknownClient: config.OAuth2UnknownClientDefault,
		Clients: []config.OAuth2Client{{
			DownstreamClientID: "other",
			ClientID:           "mapped-client",
		}},
		AuthParams: map[string]string{"prompt": "consent"},
	})
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, mappedLoginRequest(t), srv)

	require.Equal(t, http.StatusFound, rw.Code)
	q := upstreamAuthQuery(t, rw)
	require.Equal(t, "shared", q.Get("client_id"))
	require.Equal(t, "consent", q.Get("prompt"))
}

func TestLoginEndpoint_NoAuthParams(t *testing.T) {
	h, _ := newMappedLoginHandler(t)
	srv := mappedLoginServer(&config.OAuth2{
		ClientID:     "shared",
		ClientSecret: "shared-secret",
		AuthURL:      "https://auth.example.com/auth",
		TokenURL:     "https://auth.example.com/token",
	})
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, mappedLoginRequest(t), srv)

	require.Equal(t, http.StatusFound, rw.Code)
	require.NotContains(t, upstreamAuthQuery(t, rw), "prompt")
}

// マッピングした上流クライアントは、コールバックで作られる refresh_session まで届く。
func TestLoginEndpoint_MappedUpstreamClientReachesRefreshSession(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "upstream-access-token",
			"token_type":    "Bearer",
			"refresh_token": "upstream-refresh-token",
		})
	}))
	defer tokenSrv.Close()

	regJSON, err := json.Marshal(ClientRegistration{
		ClientID:     "client1",
		RedirectURIs: []string{"https://app.example.com/callback"},
	})
	require.NoError(t, err)
	st := newMockStore(map[string]string{"oauth_client:client1": string(regJSON)})
	h := NewAuthHandler(st, config.Servers{},
		WithEncryptKey(make([]byte, 32)), WithHTTPClient(http.DefaultClient))
	srv := mappedLoginServer(&config.OAuth2{
		ClientID:     "shared",
		ClientSecret: "shared-secret",
		AuthURL:      "https://auth.example.com/auth",
		TokenURL:     tokenSrv.URL + "/token",
		Clients: []config.OAuth2Client{{
			DownstreamClientID: "client1",
			ClientID:           "mapped-client",
			ClientSecret:       "mapped-secret",
		}},
	})

	rw := httptest.NewRecorder()
	h.LoginEndpoint(rw, mappedLoginRequest(t), srv)
	require.Equal(t, http.StatusFound, rw.Code)
	sessionID := upstreamAuthQuery(t, rw).Get("state")
	require.NotEmpty(t, sessionID)

	cbReq := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/myserver/auth/callback?state="+sessionID+"&code=upstream-code", nil)
	cbReq.Host = "gateway.example.com"
	cbRW := httptest.NewRecorder()
	h.CallbackEndpoint(cbRW, cbReq, srv)
	require.Equal(t, http.StatusFound, cbRW.Code)

	encrypted, ok := st.data["refresh_session:upstream-refresh-token"]
	require.True(t, ok, "refresh_session should be stored")
	rtSessionJSON, err := h.decryptToken(encrypted)
	require.NoError(t, err)
	var rtSession RefreshTokenSession
	require.NoError(t, json.Unmarshal(rtSessionJSON, &rtSession))
	require.Equal(t, "mapped-client", rtSession.OAuth2ClientID)
	require.Equal(t, "mapped-secret", rtSession.OAuth2ClientSecret)
}

// manifold 側に同意済みスキップの経路は無く、同じ client_id でも毎回上流へ送る。
func TestLoginEndpoint_AlwaysRedirectsUpstream(t *testing.T) {
	h, _ := newMappedLoginHandler(t)
	srv := mappedLoginServer(&config.OAuth2{
		ClientID:     "shared",
		ClientSecret: "shared-secret",
		AuthURL:      "https://auth.example.com/auth",
		TokenURL:     "https://auth.example.com/token",
	})

	for range 2 {
		rw := httptest.NewRecorder()
		h.LoginEndpoint(rw, mappedLoginRequest(t), srv)
		require.Equal(t, http.StatusFound, rw.Code)
		require.Contains(t, rw.Header().Get("Location"), "https://auth.example.com/auth")
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
		Name: "myserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/myserver/auth/login?client_id=client1&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st",
		nil,
	)
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

	var metaServerURL, mcpServerURL string
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			// RFC 9728 §3.3: resource はリクエスト先 URL (MCP バックエンド) と一致する
			"resource":              mcpServerURL,
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
	mcpServerURL = mcpSrv.URL

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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token=unknown"),
	)
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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token=bad-token"),
	)
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
	upstreamSrv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
		}),
	)
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
	upstreamSrv := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		}),
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader("invalid json"),
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader(reqBody),
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/test/auth/clients",
		strings.NewReader(reqBody),
	)
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
	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/register",
		strings.NewReader("invalid json"),
	)
	rw := httptest.NewRecorder()

	h.RegisterClientEndpointByClaudeCode(rw, req)

	require.Equal(t, http.StatusBadRequest, rw.Code)
	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(
		spans,
		"httphandler/AuthHandler/RegisterClientEndpointByClaudeCode",
	)
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

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token=unknown"),
	)
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

	_, err := sendProbeRequest(context.Background(), srv.URL, nil)
	require.Error(t, err)

	spans := exporter.GetSpans()
	span, found := spanByNameSuffix(spans, "httphandler/AuthHandler/sendProbeRequest")
	require.True(t, found, "span not found")
	require.Equal(t, codes.Error, span.Status.Code)
}

func TestSendProbeRequest_Returns401_SpanNoError(t *testing.T) {
	exporter := setupTracerProvider(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().
			Set("Www-Authenticate", `Bearer resource_metadata="https://example.com/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := sendProbeRequest(context.Background(), srv.URL, nil)
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

	_, err := getAuthorizationServers(
		context.Background(),
		srv.URL,
		"http://example.com/resource",
		http.DefaultClient,
	)
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

	_, err := getAuthorizationServers(
		context.Background(),
		srv.URL,
		"http://example.com/resource",
		http.DefaultClient,
	)
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

// --- DCR クライアントと登録元 MCP サーバーの束縛 ---

func TestClientAllowedForServer(t *testing.T) {
	tests := []struct {
		name          string
		source        string
		mcpServerName string
		srv           *config.Server
		want          bool
	}{
		{"dcr client on its own server", ClientSourceDCR, "server-a",
			&config.Server{Name: "server-a"}, true},
		{"dcr client on another server", ClientSourceDCR, "server-a",
			&config.Server{Name: "server-b"}, false},
		{"cimd client", ClientSourceCIMD, "",
			&config.Server{Name: "server-b"}, true},
		{"dcr client without a recorded server", ClientSourceDCR, "",
			&config.Server{Name: "server-b"}, true},
		{"server resolved from the registration", ClientSourceDCR, "server-a", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := &StoreClientRegistration{Source: tt.source, MCPServerName: tt.mcpServerName}
			require.Equal(t, tt.want, clientAllowedForServer(reg, tt.srv))
		})
	}
}

// --- LoginEndpoint: DCR クライアントと登録元 MCP サーバーの束縛 ---

// newBoundClientHandler は server-a で DCR 登録された client1 を持つハンドラを返す。
func newBoundClientHandler(t *testing.T, source string) (*AuthHandler, *mockStore) {
	t.Helper()
	regJSON, err := json.Marshal(StoreClientRegistration{
		ClientRegistration: ClientRegistration{
			ClientID:     "client1",
			ClientName:   "Client One",
			RedirectURIs: []string{"https://app.example.com/callback"},
		},
		MCPServerName: "server-a",
		Source:        source,
	})
	require.NoError(t, err)
	st := newMockStore(map[string]string{"oauth_client:client1": string(regJSON)})
	return NewAuthHandler(st, config.Servers{}), st
}

func boundClientServer(name string) *config.Server {
	return &config.Server{
		Name: name,
		OAuth2: &config.OAuth2{
			ClientID:     "upstream",
			ClientSecret: "upstream-secret",
			AuthURL:      "https://auth.example.com/auth",
			TokenURL:     "https://auth.example.com/token",
		},
	}
}

func boundClientLoginRequest(t *testing.T, serverName string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		fmt.Sprintf("/%s/auth/login?client_id=client1"+
			"&redirect_uri=https://app.example.com/callback"+
			"&code_challenge=abc&code_challenge_method=S256&state=st", serverName), nil)
	req.Host = "gateway.example.com"
	return req
}

func TestLoginEndpoint_DCRClientRejectedOnAnotherServer(t *testing.T) {
	h, _ := newBoundClientHandler(t, ClientSourceDCR)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, boundClientLoginRequest(t, "server-b"), boundClientServer("server-b"))

	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.Contains(t, rw.Body.String(), "invalid_client")
	require.Empty(t, rw.Header().Get("Location"),
		"登録元以外のサーバーの上流認可エンドポイントへ進んではいけない")
}

func TestLoginEndpoint_DCRClientAllowedOnRegisteringServer(t *testing.T) {
	h, st := newBoundClientHandler(t, ClientSourceDCR)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, boundClientLoginRequest(t, "server-a"), boundClientServer("server-a"))

	require.Equal(t, http.StatusFound, rw.Code)
	require.Contains(t, rw.Header().Get("Location"), "https://auth.example.com/auth")
	require.Equal(t, "server-a", storedAuthSession(t, st).MCPServerName)
}

// 束縛の検証は redirect_uri の照合より前に行うため、未登録の redirect_uri でも
// 400 invalid_redirect_uri ではなく 401 invalid_client になる。
func TestLoginEndpoint_DCRClientBindingCheckedBeforeRedirectURI(t *testing.T) {
	h, _ := newBoundClientHandler(t, ClientSourceDCR)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/server-b/auth/login?client_id=client1"+
			"&redirect_uri=https://evil.example.com/cb"+
			"&code_challenge=abc&code_challenge_method=S256&state=st", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, boundClientServer("server-b"))

	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.Contains(t, rw.Body.String(), "invalid_client")
}

// Source を持たない古い登録レコードは resolveClient が DCR とみなすため、束縛も効く。
func TestLoginEndpoint_LegacyRegistrationWithoutSourceIsBound(t *testing.T) {
	h, _ := newBoundClientHandler(t, "")
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, boundClientLoginRequest(t, "server-b"), boundClientServer("server-b"))

	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.Contains(t, rw.Body.String(), "invalid_client")
}

// CIMD で解決したクライアントは MCPServerName を持たず、MCP サーバーを横断して使える。
func TestLoginEndpoint_CIMDClientIsNotBoundToServer(t *testing.T) {
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()))
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		boundClientServer("server-b"),
	)

	require.Equal(t, http.StatusFound, rw.Code)
	require.Contains(t, rw.Header().Get("Location"), "https://auth.example.com/auth")
}

// --- LoginEndpoint: client_id 必須・サーバー解決 ---

func TestLoginEndpoint_EmptyClientID(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/server-a/auth/login?redirect_uri=https://app.example.com/callback"+
			"&code_challenge=abc&code_challenge_method=S256&state=st", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, boundClientServer("server-a"))

	require.Equal(t, http.StatusBadRequest, rw.Code)
	require.Contains(t, rw.Body.String(), "invalid_client_id")
}

func TestLoginEndpoint_ServerResolvedFromRegistration(t *testing.T) {
	h, _ := newBoundClientHandler(t, ClientSourceDCR)
	h.servers = config.Servers{"server-a": boundClientServer("server-a")}
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, boundClientLoginRequest(t, "server-a"), nil)

	require.Equal(t, http.StatusFound, rw.Code)
	require.Contains(t, rw.Header().Get("Location"), "https://auth.example.com/auth")
}

func TestLoginEndpoint_OAuth2NotConfiguredForOpenAPIServer(t *testing.T) {
	h, _ := newBoundClientHandler(t, ClientSourceDCR)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		boundClientLoginRequest(t, "server-a"),
		&config.Server{Name: "server-a", Spec: "local/spec.json"},
	)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
	require.Contains(t, rw.Body.String(), "oauth2 not configured for this server")
}

// --- discoverOAuth2: resource の検証 (RFC 9728 §3.3) ---

// newDiscoverTestAuthServer は DCR 可能な認可サーバーを起動してその URL を返す。
func newDiscoverTestAuthServer(t *testing.T) string {
	t.Helper()
	var authURL string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"issuer":                           authURL,
				"authorization_endpoint":           authURL + "/auth",
				"token_endpoint":                   authURL + "/token",
				"registration_endpoint":            authURL + "/register",
				"response_types_supported":         []string{"code"},
				"grant_types_supported":            []string{"authorization_code"},
				"code_challenge_methods_supported": []string{"S256"},
			})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"client_id": "cid"}) //nolint: errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(authSrv.Close)
	authURL = authSrv.URL
	return authURL
}

// newDiscoverTestBackend は path 配下で MCP を提供するバックエンドを起動する。
// wwwAuth が空でなければ 401 に WWW-Authenticate を付与する。
// resource は PRM が返す resource (空なら srv.URL)、metaPaths は PRM を提供する well-known パス。
func newDiscoverTestBackend(
	t *testing.T,
	authURL, mcpPath, resource string,
	metaPaths []string,
	withResourceMetadata bool,
) *config.Server {
	t.Helper()
	var baseURL string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, mp := range metaPaths {
			if r.URL.Path == mp {
				res := resource
				if res == "" {
					res = baseURL + mcpPath
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
					"resource":              res,
					"authorization_servers": []string{authURL},
				})
				return
			}
		}
		if r.URL.Path == mcpPath {
			if withResourceMetadata {
				w.Header().Set("Www-Authenticate", fmt.Sprintf(
					`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, baseURL))
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(backend.Close)
	baseURL = backend.URL
	return &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       baseURL + mcpPath,
	}
}

func newDiscoverTestHandler() *AuthHandler {
	return &AuthHandler{
		servers: config.Servers{"testsrv": &config.Server{Name: "testsrv"}},
	}
}

// WWW-Authenticate がパス無しの well-known を指し、resource が srv.URL (パス付き) の場合は成功する
func TestDiscoverOAuth2_WWWAuthenticateRootMetaWithPathResource(t *testing.T) {
	authURL := newDiscoverTestAuthServer(t)
	srv := newDiscoverTestBackend(t, authURL, "/mcp", "",
		[]string{"/.well-known/oauth-protected-resource"}, true)

	result, err := newDiscoverTestHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "cid", result.ClientID)
	require.Equal(t, authURL+"/token", result.TokenURL)
}

// WWW-Authenticate 由来で resource が srv.URL と一致しない場合は失敗する (なりすまし防止)
func TestDiscoverOAuth2_WWWAuthenticateResourceMismatch(t *testing.T) {
	authURL := newDiscoverTestAuthServer(t)
	srv := newDiscoverTestBackend(t, authURL, "/mcp", "https://evil.example.com/mcp",
		[]string{"/.well-known/oauth-protected-resource"}, true)

	_, err := newDiscoverTestHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.Error(t, err)
}

// resource_metadata 無し: パス挿入 well-known (RFC 9728 §3.1) で検出できる
func TestDiscoverOAuth2_FallbackPathInsertedWellKnown(t *testing.T) {
	authURL := newDiscoverTestAuthServer(t)
	srv := newDiscoverTestBackend(t, authURL, "/mcp", "",
		[]string{"/.well-known/oauth-protected-resource/mcp"}, false)

	result, err := newDiscoverTestHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "cid", result.ClientID)
}

// resource_metadata 無し: ホスト直下の well-known にフォールバックし、従来どおり逆変換 (ホストのみ) で検証する
func TestDiscoverOAuth2_FallbackRootWellKnown(t *testing.T) {
	authURL := newDiscoverTestAuthServer(t)
	// ルート直下の well-known の resource は逆変換結果 (ホストのみ) と一致する必要がある
	var baseURL string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"resource":              baseURL,
				"authorization_servers": []string{authURL},
			})
		case "/mcp":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()
	baseURL = backend.URL
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       baseURL + "/mcp",
	}

	result, err := newDiscoverTestHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "cid", result.ClientID)
}

// resource_metadata 無し: resource が逆変換結果と異なる場合は従来どおり失敗する
func TestDiscoverOAuth2_FallbackResourceMismatch(t *testing.T) {
	authURL := newDiscoverTestAuthServer(t)
	srv := newDiscoverTestBackend(t, authURL, "/mcp", "https://evil.example.com/mcp",
		[]string{"/.well-known/oauth-protected-resource"}, false)

	_, err := newDiscoverTestHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.Error(t, err)
}

// --- 秘匿値（リフレッシュトークン・認可コード・state）がログ・スパンに漏れないこと ---

// captureLogs はテスト中の slog 出力をバッファに集め、終了時に元のロガーへ戻す。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(
		slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
	))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// spanErrorTexts はスパンに記録されたエラー関連の文字列（ステータス説明・イベント属性）を返す。
func spanErrorTexts(span tracetest.SpanStub) []string {
	texts := []string{span.Status.Description}
	for _, ev := range span.Events {
		texts = append(texts, ev.Name)
		for _, kv := range ev.Attributes {
			texts = append(texts, kv.Value.String())
		}
	}
	return texts
}

func requireSpanErrorWithoutSecret(t *testing.T, span tracetest.SpanStub, secret string) {
	t.Helper()
	require.Equal(t, codes.Error, span.Status.Code)
	require.NotEmpty(t, span.Status.Description)
	for _, text := range spanErrorTexts(span) {
		require.NotContains(t, text, secret, "span must not contain the secret value")
	}
}

func TestTokenEndpoint_RefreshToken_SessionNotFound_DoesNotLeakToken(t *testing.T) {
	logs := captureLogs(t)
	exporter := setupTracerProvider(t)

	const refreshToken = "dummy-upstream-refresh-token-AAAABBBBCCCC"
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=refresh_token&refresh_token="+refreshToken),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)

	logText := logs.String()
	require.Contains(t, logText, "refresh session not found")
	require.Contains(t, logText, "refresh_token_fp="+util.Fingerprint(refreshToken))
	require.NotContains(t, logText, refreshToken)

	span, found := spanByNameSuffix(
		exporter.GetSpans(),
		"httphandler/AuthHandler/handleRefreshTokenGrant",
	)
	require.True(t, found, "span not found")
	requireSpanErrorWithoutSecret(t, span, refreshToken)
	require.Contains(t, span.Status.Description, store.ErrNotFound.Error())
}

func TestTokenEndpoint_AuthCodeNotFound_DoesNotLeakCode(t *testing.T) {
	logs := captureLogs(t)
	exporter := setupTracerProvider(t)

	const code = "dummy-authorization-code-AAAABBBBCCCC"
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/testserver/auth/token",
		strings.NewReader("grant_type=authorization_code&code="+code+"&code_verifier=verifier"),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rw := httptest.NewRecorder()

	h.TokenEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)

	logText := logs.String()
	require.Contains(t, logText, "code_fp="+util.Fingerprint(code))
	require.NotContains(t, logText, code)

	span, found := spanByNameSuffix(exporter.GetSpans(), "httphandler/AuthHandler/TokenEndpoint")
	require.True(t, found, "span not found")
	requireSpanErrorWithoutSecret(t, span, code)
}

func TestCallbackEndpoint_SessionNotFound_DoesNotLeakState(t *testing.T) {
	logs := captureLogs(t)
	exporter := setupTracerProvider(t)

	const state = "dummy-oauth-state-AAAABBBBCCCC"
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/testserver/auth/callback?state="+state+"&code=somecode",
		nil,
	)
	rw := httptest.NewRecorder()

	h.CallbackEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)

	logText := logs.String()
	require.Contains(t, logText, "state_fp="+util.Fingerprint(state))
	require.NotContains(t, logText, state)

	span, found := spanByNameSuffix(exporter.GetSpans(), "httphandler/AuthHandler/CallbackEndpoint")
	require.True(t, found, "span not found")
	requireSpanErrorWithoutSecret(t, span, state)
}

func TestSanitizeStoreErr(t *testing.T) {
	const secret = "refresh_session:super-secret-token"
	notFound := fmt.Errorf("key not found: %s: %w", secret, store.ErrNotFound)

	got := sanitizeStoreErr("refresh session not found", notFound)
	require.ErrorIs(t, got, store.ErrNotFound)
	require.NotContains(t, got.Error(), secret)
	require.Equal(t, "refresh session not found: "+store.ErrNotFound.Error(), got.Error())

	other := fmt.Errorf("sqlite Get: %w", context.DeadlineExceeded)
	got = sanitizeStoreErr("refresh session not found", other)
	require.ErrorIs(t, got, context.DeadlineExceeded)
	require.NotErrorIs(t, got, store.ErrNotFound)
}

// --- discoverOAuth2: 上流 CIMD ---

const testUpstreamCIMDClientID = "https://cdn.example.com/manifold/github.json"

// newCIMDDiscoverFixture は認可サーバー（メタデータのみ、registration endpoint は
// withRegistration のときだけ）と、それを指す MCP バックエンドを立てる。
// DCR が呼ばれた回数を dcrCalls に数える。
func newCIMDDiscoverFixture(
	t *testing.T,
	cimdSupported, withRegistration bool,
) (srv *config.Server, dcrCalls *atomic.Int64) {
	t.Helper()
	dcrCalls = &atomic.Int64{}
	var authServerURL string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			meta := map[string]any{
				"issuer":                           authServerURL,
				"authorization_endpoint":           authServerURL + "/auth",
				"token_endpoint":                   authServerURL + "/token",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"S256"},
			}
			if cimdSupported {
				meta["client_id_metadata_document_supported"] = true
			}
			if withRegistration {
				meta["registration_endpoint"] = authServerURL + "/register"
			}
			json.NewEncoder(w).Encode(meta) //nolint: errcheck
		case "/register":
			dcrCalls.Add(1)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
				"client_id":     "dcr-client-id",
				"client_secret": "dcr-client-secret",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(authSrv.Close)
	authServerURL = authSrv.URL
	return newDiscoverTestBackendFor(t, []string{authServerURL}), dcrCalls
}

// newDiscoverTestBackendFor は authorization_servers を返す PRM と、それを指す
// 401 の MCP バックエンドを立てて、バックエンド向けの config.Server を返す。
func newDiscoverTestBackendFor(t *testing.T, authServers []string) *config.Server {
	t.Helper()
	var mcpURL string
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint: errcheck
			"resource":              mcpURL,
			"authorization_servers": authServers,
		})
	}))
	t.Cleanup(metaSrv.Close)
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().
			Set("Www-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s"`, metaSrv.URL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcpSrv.Close)
	mcpURL = mcpSrv.URL
	return &config.Server{Name: "testsrv", Transport: config.MCPTransportHTTP, URL: mcpURL}
}

func newCIMDDiscoverHandler() *AuthHandler {
	return &AuthHandler{servers: config.Servers{"testsrv": &config.Server{Name: "testsrv"}}}
}

// 上流が CIMD 対応なら registration endpoint が無くても cimdClientID が使われ、secret は空になる
func TestDiscoverOAuth2_CIMD_NoRegistrationEndpoint(t *testing.T) {
	srv, dcrCalls := newCIMDDiscoverFixture(t, true, false)
	srv.CIMDClientID = testUpstreamCIMDClientID

	result, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, testUpstreamCIMDClientID, result.ClientID)
	require.Empty(t, result.ClientSecret)
	require.Zero(t, dcrCalls.Load())
	// secret が空でも config の検証を通る
	require.NoError(t, result.ValidateWithContext(t.Context()))
}

// 上流が CIMD 対応で DCR も可能なら CIMD を優先する
func TestDiscoverOAuth2_CIMD_PreferredOverDCR(t *testing.T) {
	srv, dcrCalls := newCIMDDiscoverFixture(t, true, true)
	srv.CIMDClientID = testUpstreamCIMDClientID

	result, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, testUpstreamCIMDClientID, result.ClientID)
	require.Empty(t, result.ClientSecret)
	require.Zero(t, dcrCalls.Load())
}

// cimdClientID 未指定なら上流が CIMD 対応でも DCR にフォールバックする
func TestDiscoverOAuth2_CIMD_UnsetFallsBackToDCR(t *testing.T) {
	srv, dcrCalls := newCIMDDiscoverFixture(t, true, true)

	result, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "dcr-client-id", result.ClientID)
	require.Equal(t, "dcr-client-secret", result.ClientSecret)
	require.EqualValues(t, 1, dcrCalls.Load())
}

// 上流が CIMD 非対応なら cimdClientID を設定していても DCR にフォールバックする
func TestDiscoverOAuth2_CIMD_UnsupportedUpstreamFallsBackToDCR(t *testing.T) {
	srv, dcrCalls := newCIMDDiscoverFixture(t, false, true)
	srv.CIMDClientID = testUpstreamCIMDClientID

	result, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "dcr-client-id", result.ClientID)
	require.Equal(t, "dcr-client-secret", result.ClientSecret)
	require.EqualValues(t, 1, dcrCalls.Load())
}

// newMetaOnlyAuthServer は認可サーバーのメタデータを返すサーバーを立てて URL を返す。
// withRegistration のときだけ registration endpoint を広告し、DCR は clientID を返す。
func newMetaOnlyAuthServer(t *testing.T, withRegistration bool, clientID string) string {
	t.Helper()
	var u string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			meta := map[string]any{
				"issuer":                           u,
				"authorization_endpoint":           u + "/auth",
				"token_endpoint":                   u + "/token",
				"response_types_supported":         []string{"code"},
				"code_challenge_methods_supported": []string{"S256"},
			}
			if withRegistration {
				meta["registration_endpoint"] = u + "/register"
			}
			json.NewEncoder(w).Encode(meta) //nolint: errcheck
		case "/register":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"client_id": clientID}) //nolint: errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	u = s.URL
	return u
}

// 1 件目の認可サーバーが失敗し 2 件目が成功すれば成功扱いになる
func TestDiscoverOAuth2_FirstAuthServerFailsSecondSucceeds(t *testing.T) {
	authServerURL := newMetaOnlyAuthServer(t, true, "second-client")
	broken := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(broken.Close)

	srv := newDiscoverTestBackendFor(t, []string{broken.URL, authServerURL})
	result, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, authServerURL+"/token", result.TokenURL)
	require.Equal(t, "second-client", result.ClientID)
}

// 1 件目は取得できても登録手段が無ければ、DCR 可能な 2 件目を使う
func TestDiscoverOAuth2_SkipsAuthServerWithoutRegistration(t *testing.T) {
	first := newMetaOnlyAuthServer(t, false, "")
	second := newMetaOnlyAuthServer(t, true, "second-client")

	srv := newDiscoverTestBackendFor(t, []string{first, second})
	result, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, second+"/token", result.TokenURL)
	require.Equal(t, "second-client", result.ClientID)
}

// すべての認可サーバーに登録手段が無ければエラーを返し、キャッシュしない
func TestDiscoverOAuth2_NoRegistrationMethodFails(t *testing.T) {
	first := newMetaOnlyAuthServer(t, false, "")
	second := newMetaOnlyAuthServer(t, false, "")

	srv := newDiscoverTestBackendFor(t, []string{first, second})
	h := newCIMDDiscoverHandler()
	_, err := h.discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "neither cimd nor dynamic client registration")
	require.Nil(t, h.servers["testsrv"].OAuth2)
}

// すべての認可サーバーが失敗すれば、最後のエラーを返す
func TestDiscoverOAuth2_AllAuthServersFail(t *testing.T) {
	broken := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(broken.Close)
	srv := newDiscoverTestBackendFor(t, []string{broken.URL})

	_, err := newCIMDDiscoverHandler().discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth server metadata")
}

// probe リクエストは h.httpClient を通る（SSRF 対策済みクライアントの差し替えが効く）
func TestDiscoverOAuth2_ProbeUsesHandlerHTTPClient(t *testing.T) {
	var used atomic.Int64
	h := NewAuthHandler(
		newMockStore(map[string]string{}),
		config.Servers{},
		WithHTTPClient(
			&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				used.Add(1)
				return nil, errors.New("blocked")
			})},
		),
	)
	_, err := h.discoverOAuth2(t.Context(),
		&config.Server{Name: "x", URL: "http://mcp.example.com/mcp"}, "http://gateway.example.com")
	require.Error(t, err)
	require.EqualValues(t, 1, used.Load())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
