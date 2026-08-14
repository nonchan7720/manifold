package httphandler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// --- isCIMDClientID ---

func TestIsCIMDClientID(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		want     bool
	}{
		{
			"valid https URL with path",
			"https://client.example.com/oauth/client-metadata.json",
			true,
		},
		{"http scheme", "http://client.example.com/metadata.json", false},
		{"https root path", "https://client.example.com/", false},
		{"https no path", "https://client.example.com", false},
		{"DCR random ID", "aBcDeFgHiJkLmNoPqRsTuVwXyZ012345", false},
		{"empty", "", false},
		{"with fragment", "https://client.example.com/meta#frag", false},
		{"with userinfo", "https://user:pass@client.example.com/meta", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isCIMDClientID(tt.clientID))
		})
	}
}

// --- serverNameFromResource ---

func TestServerNameFromResource(t *testing.T) {
	const baseURL = "https://gateway.example.com"
	tests := []struct {
		name     string
		resource string
		want     string
	}{
		{"valid mcp resource", "https://gateway.example.com/mcp/myserver", "myserver"},
		{"trailing slash", "https://gateway.example.com/mcp/myserver/", "myserver"},
		{"host case insensitive", "https://Gateway.Example.COM/mcp/myserver", "myserver"},
		{"no server name", "https://gateway.example.com/mcp", ""},
		{"different path", "https://gateway.example.com/api/myserver", ""},
		{"deep path", "https://gateway.example.com/mcp/a/b", ""},
		// audience 検証: ゲートウェイ以外のホストは解決しない
		{"different host", "https://evil.example.com/mcp/myserver", ""},
		{"different port", "https://gateway.example.com:8443/mcp/myserver", ""},
		// デフォルトポートは明示されていても同一オリジンとして扱う
		{"explicit default port", "https://gateway.example.com:443/mcp/myserver", "myserver"},
		// audience 検証: スキームが一致しない resource は解決しない
		{"http scheme", "http://gateway.example.com/mcp/myserver", ""},
		{"scheme relative", "//gateway.example.com/mcp/myserver", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, serverNameFromResource(tt.resource, baseURL))
		})
	}
}

// --- clientMetadataDocumentURL ---

func TestClientMetadataDocumentURL(t *testing.T) {
	require.Equal(t,
		"https://gateway.example.com/mysrv/auth/client-metadata.json",
		clientMetadataDocumentURL("https://gateway.example.com", "mysrv"))
	// CIMD の client_id は https 必須のため http ゲートウェイでは空文字
	require.Empty(t, clientMetadataDocumentURL("http://gateway.example.com", "mysrv"))
	require.Empty(t, clientMetadataDocumentURL("://bad", "mysrv"))
}

// --- validateCIMDDocument ---

func TestValidateCIMDDocument(t *testing.T) {
	clientID := "https://client.example.com/meta.json"
	valid := func() *ClientIDMetadataDocument {
		return &ClientIDMetadataDocument{
			ClientID:     clientID,
			RedirectURIs: []string{"https://client.example.com/callback"},
		}
	}

	t.Run("valid", func(t *testing.T) {
		require.NoError(t, validateCIMDDocument(valid(), clientID))
	})
	t.Run("client_id mismatch", func(t *testing.T) {
		doc := valid()
		doc.ClientID = "https://other.example.com/meta.json"
		require.Error(t, validateCIMDDocument(doc, clientID))
	})
	t.Run("no redirect_uris", func(t *testing.T) {
		doc := valid()
		doc.RedirectURIs = nil
		require.Error(t, validateCIMDDocument(doc, clientID))
	})
	t.Run("invalid redirect_uri scheme", func(t *testing.T) {
		doc := valid()
		doc.RedirectURIs = []string{"http://evil.example.com/cb"}
		require.Error(t, validateCIMDDocument(doc, clientID))
	})
	t.Run("localhost redirect_uri allowed", func(t *testing.T) {
		doc := valid()
		doc.RedirectURIs = []string{"http://localhost:8080/cb"}
		require.NoError(t, validateCIMDDocument(doc, clientID))
	})
	t.Run("token_endpoint_auth_method none allowed", func(t *testing.T) {
		doc := valid()
		doc.TokenEndpointAuthMethod = "none"
		require.NoError(t, validateCIMDDocument(doc, clientID))
	})
	t.Run("confidential auth method rejected", func(t *testing.T) {
		doc := valid()
		doc.TokenEndpointAuthMethod = "client_secret_basic"
		require.Error(t, validateCIMDDocument(doc, clientID))
	})
}

// --- fetchClientIDMetadata ---

// newCIMDTestServer は指定ハンドラで TLS サーバーを起動し、
// そのサーバーを信頼する httpClient を持つ AuthHandler を返す。
func newCIMDTestServer(
	t *testing.T,
	handler func(clientID string) http.HandlerFunc,
) (*AuthHandler, string, *mockStore) {
	t.Helper()
	var clientID string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler(clientID)(w, r)
	}))
	t.Cleanup(srv.Close)
	clientID = srv.URL + "/oauth/client-metadata.json"
	st := newMockStore(map[string]string{})
	h := &AuthHandler{store: st, servers: config.Servers{}, cimdHTTPClient: srv.Client()}
	return h, clientID, st
}

func TestFetchClientIDMetadata_Success(t *testing.T) {
	h, clientID, st := newCIMDTestServer(t, func(clientID string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
				ClientID:     clientID,
				ClientName:   "Example Client",
				RedirectURIs: []string{"https://client.example.com/callback"},
			})
		}
	})

	doc, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.NoError(t, err)
	require.Equal(t, clientID, doc.ClientID)
	require.Equal(t, "Example Client", doc.ClientName)
	require.Equal(t, []string{"https://client.example.com/callback"}, doc.RedirectURIs)

	// 検証済みドキュメントがキャッシュされている
	_, ok := st.data[cimdCachePrefix+clientID]
	require.True(t, ok, "validated CIMD document should be cached")
}

func TestFetchClientIDMetadata_CacheHit(t *testing.T) {
	// 到達不能な URL でもキャッシュがあれば成功する
	clientID := "https://unreachable.example.com/oauth/client-metadata.json"
	doc := ClientIDMetadataDocument{
		ClientID:     clientID,
		RedirectURIs: []string{"https://client.example.com/callback"},
	}
	docJSON, _ := json.Marshal(doc)
	st := newMockStore(map[string]string{
		cimdCachePrefix + clientID: string(docJSON),
	})
	h := &AuthHandler{store: st, servers: config.Servers{}}

	got, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.NoError(t, err)
	require.Equal(t, clientID, got.ClientID)
}

func TestFetchClientIDMetadata_ClientIDMismatch(t *testing.T) {
	h, clientID, _ := newCIMDTestServer(t, func(_ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
				ClientID:     "https://other.example.com/meta.json",
				RedirectURIs: []string{"https://client.example.com/callback"},
			})
		}
	})

	_, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client_id does not match")
}

func TestFetchClientIDMetadata_WrongContentType(t *testing.T) {
	h, clientID, _ := newCIMDTestServer(t, func(_ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html></html>"))
		}
	})

	_, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "content-type")
}

func TestFetchClientIDMetadata_NotFound(t *testing.T) {
	h, clientID, _ := newCIMDTestServer(t, func(_ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}
	})

	_, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.Error(t, err)
}

func TestFetchClientIDMetadata_NonCIMDClientID(t *testing.T) {
	h := &AuthHandler{store: newMockStore(map[string]string{}), servers: config.Servers{}}
	_, err := h.fetchClientIDMetadata(t.Context(), "not-a-url")
	require.Error(t, err)
}

func TestFetchClientIDMetadata_TooLarge(t *testing.T) {
	h, clientID, _ := newCIMDTestServer(t, func(_ string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			// cimdMaxBodySize (1MB) を超えるボディを返す
			_, _ = w.Write([]byte(`{"client_id":"`))
			filler := strings.Repeat("a", 64*1024)
			for range 20 {
				_, _ = w.Write([]byte(filler))
			}
			_, _ = w.Write([]byte(`"}`))
		}
	})

	_, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "size limit")
}

func TestFetchClientIDMetadata_RedirectRejected(t *testing.T) {
	h, clientID, _ := newCIMDTestServer(t, func(clientID string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/redirected" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
					ClientID:     clientID,
					RedirectURIs: []string{"https://client.example.com/callback"},
				})
				return
			}
			http.Redirect(w, r, "/redirected", http.StatusFound)
		}
	})

	// リダイレクトは追従せずエラーになる
	_, err := h.fetchClientIDMetadata(t.Context(), clientID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected status")
}

// --- MetadataEndpoint: CIMD サポートの広告 ---

func TestMetadataEndpoint_CIMDSupported(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "testserver"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
	require.Equal(t, true, body["client_id_metadata_document_supported"])
}

// --- ClientMetadataDocument ---

func TestClientMetadataDocument(t *testing.T) {
	h := &AuthHandler{}
	srv := &config.Server{Name: "mysrv"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/mysrv/auth/client-metadata.json", nil)
	req.Host = "gateway.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rw := httptest.NewRecorder()

	h.ClientMetadataDocument(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)
	require.Equal(t, "application/json", rw.Header().Get("Content-Type"))

	var doc ClientIDMetadataDocument
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &doc))
	require.Equal(t, "https://gateway.example.com/mysrv/auth/client-metadata.json", doc.ClientID)
	require.Equal(t, "manifold", doc.ClientName)
	require.Equal(t, []string{"https://gateway.example.com/mysrv/auth/callback"}, doc.RedirectURIs)
	require.Equal(t, "none", doc.TokenEndpointAuthMethod)
	require.Contains(t, doc.GrantTypes, "authorization_code")
	require.Contains(t, doc.GrantTypes, "refresh_token")
}

func TestClientMetadataDocument_ConfiguredBaseURL(t *testing.T) {
	// gateway.baseURL が設定されている場合は Host ヘッダーではなく設定値を使う
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{},
		WithGatewayBaseURL("https://canonical.example.com/"))
	srv := &config.Server{Name: "mysrv"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/mysrv/auth/client-metadata.json", nil)
	req.Host = "attacker.example.com"
	rw := httptest.NewRecorder()

	h.ClientMetadataDocument(rw, req, srv)

	require.Equal(t, http.StatusOK, rw.Code)
	var doc ClientIDMetadataDocument
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &doc))
	require.Equal(t, "https://canonical.example.com/mysrv/auth/client-metadata.json", doc.ClientID)
	require.Equal(
		t,
		[]string{"https://canonical.example.com/mysrv/auth/callback"},
		doc.RedirectURIs,
	)
}

func TestClientMetadataDocument_InvalidBaseURL(t *testing.T) {
	// ベース URL がパースできない場合は空の client_id を配信せずエラーにする
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{},
		WithGatewayBaseURL("://bad"))
	srv := &config.Server{Name: "mysrv"}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/mysrv/auth/client-metadata.json", nil)
	rw := httptest.NewRecorder()

	h.ClientMetadataDocument(rw, req, srv)

	require.Equal(t, http.StatusInternalServerError, rw.Code)
}

func TestClientMetadataDocument_NilServer(t *testing.T) {
	h := &AuthHandler{}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/unknown/auth/client-metadata.json", nil)
	rw := httptest.NewRecorder()

	h.ClientMetadataDocument(rw, req, nil)

	require.Equal(t, http.StatusNotFound, rw.Code)
}

// --- LoginEndpoint: CIMD クライアント ---

func TestLoginEndpoint_CIMDClient(t *testing.T) {
	docSrv := httptest.NewTLSServer(nil)
	defer docSrv.Close()
	clientID := docSrv.URL + "/oauth/client-metadata.json"
	docSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
			ClientID:     clientID,
			RedirectURIs: []string{"https://app.example.com/callback"},
		})
	})

	st := newMockStore(map[string]string{})
	h := &AuthHandler{store: st, servers: config.Servers{}, cimdHTTPClient: docSrv.Client()}
	srv := &config.Server{
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}

	target := fmt.Sprintf(
		"/testserver/auth/login?client_id=%s&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st",
		clientID,
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	// 上流 OAuth2 サーバーへリダイレクトされる
	require.Equal(t, http.StatusFound, rw.Code)

	// セッションに CIMD の client_id (URL) がそのまま保存されている
	var session AuthSession
	for k, v := range st.data {
		if strings.HasPrefix(k, "auth_session:") {
			require.NoError(t, json.Unmarshal([]byte(v), &session))
			break
		}
	}
	require.Equal(t, clientID, session.ClientID)
}

func TestLoginEndpoint_CIMDClient_MismatchedRedirectURI(t *testing.T) {
	docSrv := httptest.NewTLSServer(nil)
	defer docSrv.Close()
	clientID := docSrv.URL + "/oauth/client-metadata.json"
	docSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
			ClientID:     clientID,
			RedirectURIs: []string{"https://app.example.com/callback"},
		})
	})

	st := newMockStore(map[string]string{})
	h := &AuthHandler{store: st, servers: config.Servers{}, cimdHTTPClient: docSrv.Client()}
	srv := &config.Server{
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}

	// ドキュメントに登録されていない redirect_uri を指定
	target := fmt.Sprintf(
		"/testserver/auth/login?client_id=%s&redirect_uri=https://evil.example.com/cb&code_challenge=abc&code_challenge_method=S256",
		clientID,
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_CIMDClient_FetchFails(t *testing.T) {
	docSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer docSrv.Close()
	clientID := docSrv.URL + "/oauth/client-metadata.json"

	st := newMockStore(map[string]string{})
	h := &AuthHandler{store: st, servers: config.Servers{}, cimdHTTPClient: docSrv.Client()}
	srv := &config.Server{
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}

	target := fmt.Sprintf(
		"/testserver/auth/login?client_id=%s&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256",
		clientID,
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, srv)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestLoginEndpoint_CIMDClient_ResolvesServerFromResource(t *testing.T) {
	docSrv := httptest.NewTLSServer(nil)
	defer docSrv.Close()
	clientID := docSrv.URL + "/oauth/client-metadata.json"
	docSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
			ClientID:     clientID,
			RedirectURIs: []string{"https://app.example.com/callback"},
		})
	})

	st := newMockStore(map[string]string{})
	target := &config.Server{
		Name: "resolved",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}
	h := &AuthHandler{
		store:          st,
		servers:        config.Servers{"resolved": target},
		cimdHTTPClient: docSrv.Client(),
	}

	// グローバル /authorize 相当: srv=nil, resource パラメータでサーバーを指定
	uri := fmt.Sprintf(
		"/authorize?client_id=%s&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st&resource=%s",
		clientID,
		"https://gateway.example.com/mcp/resolved",
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, uri, nil)
	req.Host = "gateway.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, req, nil)

	require.Equal(t, http.StatusFound, rw.Code)

	var session AuthSession
	for k, v := range st.data {
		if strings.HasPrefix(k, "auth_session:") {
			require.NoError(t, json.Unmarshal([]byte(v), &session))
			break
		}
	}
	require.Equal(t, "resolved", session.MCPServerName)
}

// --- LoginEndpoint: resource とサーバーの一致検証（RFC 8707） ---

// newResourceBindingTestHandler はパス経由ログインの resource 検証テスト用に、
// CIMD クライアントと testserver を持つ AuthHandler を返す。
func newResourceBindingTestHandler(t *testing.T) (*AuthHandler, string, *config.Server) {
	t.Helper()
	docSrv := httptest.NewTLSServer(nil)
	t.Cleanup(docSrv.Close)
	clientID := docSrv.URL + "/oauth/client-metadata.json"
	docSrv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ClientIDMetadataDocument{
			ClientID:     clientID,
			RedirectURIs: []string{"https://app.example.com/callback"},
		})
	})

	h := &AuthHandler{
		store:          newMockStore(map[string]string{}),
		servers:        config.Servers{},
		cimdHTTPClient: docSrv.Client(),
	}
	srv := &config.Server{
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID: "upstream",
			AuthURL:  "https://auth.example.com/auth",
			TokenURL: "https://auth.example.com/token",
		},
	}
	return h, clientID, srv
}

func loginRequestWithResource(t *testing.T, clientID, resource string) *http.Request {
	t.Helper()
	uri := fmt.Sprintf(
		"/testserver/auth/login?client_id=%s&redirect_uri=https://app.example.com/callback&code_challenge=abc&code_challenge_method=S256&state=st&resource=%s",
		clientID,
		resource,
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, uri, nil)
	req.Host = "gateway.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")
	return req
}

func TestLoginEndpoint_ResourceMatchingPathServer(t *testing.T) {
	h, clientID, srv := newResourceBindingTestHandler(t)

	rw := httptest.NewRecorder()
	h.LoginEndpoint(rw, loginRequestWithResource(
		t, clientID, "https://gateway.example.com/mcp/testserver"), srv)

	require.Equal(t, http.StatusFound, rw.Code)
}

func TestLoginEndpoint_ResourceMismatchRejected(t *testing.T) {
	h, clientID, srv := newResourceBindingTestHandler(t)

	// パスで解決したサーバーと異なるサーバーを指す resource は拒否する
	rw := httptest.NewRecorder()
	h.LoginEndpoint(rw, loginRequestWithResource(
		t, clientID, "https://gateway.example.com/mcp/otherserver"), srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_ResourceForeignHostRejected(t *testing.T) {
	h, clientID, srv := newResourceBindingTestHandler(t)

	// ゲートウェイ以外のホストを指す resource は拒否する
	rw := httptest.NewRecorder()
	h.LoginEndpoint(rw, loginRequestWithResource(
		t, clientID, "https://evil.example.com/mcp/testserver"), srv)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

// --- discoverOAuth2: CIMD 対応の上流には CIMD を優先 ---

// newDiscoveryTestEnv は discoverOAuth2 テスト用の
// 認可サーバー / PRM / MCP バックエンドのモック一式を起動する。
func newDiscoveryTestEnv(t *testing.T, cimdSupported bool, dcrCalled *bool) (mcpURL string) {
	t.Helper()
	var authServerURL string
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		issuer := authServerURL
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			meta := map[string]any{
				"issuer":                           issuer,
				"authorization_endpoint":           issuer + "/auth",
				"token_endpoint":                   issuer + "/token",
				"registration_endpoint":            issuer + "/register",
				"response_types_supported":         []string{"code"},
				"grant_types_supported":            []string{"authorization_code"},
				"code_challenge_methods_supported": []string{"S256"},
			}
			if cimdSupported {
				meta["client_id_metadata_document_supported"] = true
			}
			_ = json.NewEncoder(w).Encode(meta)
		case "/register":
			if dcrCalled != nil {
				*dcrCalled = true
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":     "dcr-client-id",
				"client_secret": "dcr-client-secret",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(authSrv.Close)
	authServerURL = authSrv.URL

	var metaServerURL string
	metaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource":              metaServerURL,
			"authorization_servers": []string{authServerURL},
		})
	}))
	t.Cleanup(metaSrv.Close)
	metaServerURL = metaSrv.URL

	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Www-Authenticate",
			fmt.Sprintf(`Bearer resource_metadata="%s"`, metaServerURL))
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(mcpSrv.Close)
	return mcpSrv.URL
}

func TestDiscoverOAuth2_CIMD_PreferredOverDCR(t *testing.T) {
	dcrCalled := false
	mcpURL := newDiscoveryTestEnv(t, true, &dcrCalled)

	h := &AuthHandler{
		servers:        config.Servers{"testsrv": &config.Server{Name: "testsrv"}},
		gatewayBaseURL: "https://gateway.example.com",
	}
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       mcpURL,
	}

	// 設定済みの https ゲートウェイ → CIMD が使われる
	result, err := h.discoverOAuth2(t.Context(), srv, "https://gateway.example.com")
	require.NoError(t, err)
	require.Equal(
		t,
		"https://gateway.example.com/testsrv/auth/client-metadata.json",
		result.ClientID,
	)
	require.Empty(t, result.ClientSecret)
	require.False(t, dcrCalled, "DCR should not be called when CIMD is supported")
}

func TestDiscoverOAuth2_CIMD_NoConfiguredBaseURLFallsBackToDCR(t *testing.T) {
	dcrCalled := false
	mcpURL := newDiscoveryTestEnv(t, true, &dcrCalled)

	// gateway.baseURL 未設定 → Host ヘッダー由来のベース URL では CIMD を使わない
	h := &AuthHandler{
		servers: config.Servers{"testsrv": &config.Server{Name: "testsrv"}},
	}
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       mcpURL,
	}

	result, err := h.discoverOAuth2(t.Context(), srv, "https://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "dcr-client-id", result.ClientID)
	require.True(t, dcrCalled)
}

func TestDiscoverOAuth2_CIMD_HTTPGatewayFallsBackToDCR(t *testing.T) {
	dcrCalled := false
	mcpURL := newDiscoveryTestEnv(t, true, &dcrCalled)

	h := &AuthHandler{
		servers: config.Servers{"testsrv": &config.Server{Name: "testsrv"}},
	}
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       mcpURL,
	}

	// http ゲートウェイでは CIMD の client_id 要件（https）を満たせないため DCR にフォールバック
	result, err := h.discoverOAuth2(t.Context(), srv, "http://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "dcr-client-id", result.ClientID)
	require.Equal(t, "dcr-client-secret", result.ClientSecret)
	require.True(t, dcrCalled)
}

func TestDiscoverOAuth2_CIMD_NotSupportedUsesDCR(t *testing.T) {
	dcrCalled := false
	mcpURL := newDiscoveryTestEnv(t, false, &dcrCalled)

	h := &AuthHandler{
		servers: config.Servers{"testsrv": &config.Server{Name: "testsrv"}},
	}
	srv := &config.Server{
		Name:      "testsrv",
		Transport: config.MCPTransportHTTP,
		URL:       mcpURL,
	}

	result, err := h.discoverOAuth2(t.Context(), srv, "https://gateway.example.com")
	require.NoError(t, err)
	require.Equal(t, "dcr-client-id", result.ClientID)
	require.True(t, dcrCalled)
}
