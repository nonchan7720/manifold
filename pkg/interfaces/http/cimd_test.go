package httphandler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

const testCIMDClientID = "https://client.example.com/oauth/client-metadata.json"

const testCIMDRedirectURI = "https://client.example.com/callback"

// cimdTestTransport は client_id URL のホストに関係なく、すべてのリクエストを
// テストサーバーへ差し向ける。CIMD の client_id はホスト名（IP リテラル不可）
// でなければならないため、httptest サーバーの URL をそのまま client_id には使えない。
type cimdTestTransport struct {
	target *url.URL
	base   http.RoundTripper
}

func (t *cimdTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme = t.target.Scheme
	r.URL.Host = t.target.Host
	r.Host = req.URL.Host
	return t.base.RoundTrip(r)
}

func newCIMDTestHandler(
	t *testing.T,
	cimd config.CIMDConfig,
	handler http.HandlerFunc,
) (*AuthHandler, *mockStore, *atomic.Int64) {
	t.Helper()
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	st := newMockStore(map[string]string{})
	h := NewAuthHandler(st, config.Servers{},
		WithCIMD(cimd),
		WithHTTPClient(&http.Client{
			Transport: &cimdTestTransport{target: target, base: http.DefaultTransport},
		}),
	)
	return h, st, &fetches
}

func cimdDocumentHandler(doc ClientIDMetadataDocument) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}
}

func validCIMDDocument() ClientIDMetadataDocument {
	return ClientIDMetadataDocument{
		ClientID:     testCIMDClientID,
		ClientName:   "Example Client",
		RedirectURIs: []string{testCIMDRedirectURI},
	}
}

func cimdEnabled() config.CIMDConfig {
	return config.CIMDConfig{Enabled: true}.WithDefaults()
}

func cimdLoginRequest(t *testing.T, clientID, redirectURI string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		fmt.Sprintf(
			"/testserver/auth/login?client_id=%s&redirect_uri=%s"+
				"&code_challenge=abc&code_challenge_method=S256&state=st",
			url.QueryEscape(clientID), url.QueryEscape(redirectURI),
		), nil)
	req.Host = "gateway.example.com"
	return req
}

func cimdTestServer() *config.Server {
	return &config.Server{
		Name: "testserver",
		OAuth2: &config.OAuth2{
			ClientID:     "upstream",
			ClientSecret: "upstream-secret",
			AuthURL:      "https://auth.example.com/auth",
			TokenURL:     "https://auth.example.com/token",
		},
	}
}

// --- validateCIMDClientID ---

func TestValidateCIMDClientID(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		wantErr  bool
	}{
		{"https URL with path", testCIMDClientID, false},
		{"http scheme", "http://client.example.com/metadata.json", true},
		{"root path", "https://client.example.com/", true},
		{"no path", "https://client.example.com", true},
		{"DCR random id", "aBcDeFgHiJkLmNoPqRsTuVwXyZ012345", true},
		{"empty", "", true},
		{"fragment", "https://client.example.com/meta.json#frag", true},
		{"userinfo", "https://user:pass@client.example.com/meta.json", true},
		{"ipv4 host", "https://203.0.113.10/meta.json", true},
		{"ipv6 host", "https://[2001:db8::1]/meta.json", true},
		{"localhost", "https://localhost/meta.json", true},
		{"localhost with port", "https://localhost:8080/meta.json", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCIMDClientID(tt.clientID, cimdEnabled())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateCIMDClientID_AllowedOrigins(t *testing.T) {
	cfg := config.CIMDConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://client.example.com"},
	}.WithDefaults()

	require.NoError(t, validateCIMDClientID(testCIMDClientID, cfg))
	require.Error(t, validateCIMDClientID("https://evil.example.com/meta.json", cfg))
}

// --- validateCIMDDocument ---

func TestValidateCIMDDocument(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ClientIDMetadataDocument)
		wantErr bool
	}{
		{"valid", func(*ClientIDMetadataDocument) {}, false},
		{"client_id mismatch", func(d *ClientIDMetadataDocument) {
			d.ClientID = "https://other.example.com/meta.json"
		}, true},
		{"client_id trailing slash is not normalized", func(d *ClientIDMetadataDocument) {
			d.ClientID = testCIMDClientID + "/"
		}, true},
		{"no redirect_uris", func(d *ClientIDMetadataDocument) { d.RedirectURIs = nil }, true},
		{"invalid redirect_uri", func(d *ClientIDMetadataDocument) {
			d.RedirectURIs = []string{"http://evil.example.com/cb"}
		}, true},
		{"localhost redirect_uri", func(d *ClientIDMetadataDocument) {
			d.RedirectURIs = []string{"http://localhost:8080/cb"}
		}, false},
		{"auth method none", func(d *ClientIDMetadataDocument) {
			d.TokenEndpointAuthMethod = "none"
		}, false},
		{"auth method client_secret_post", func(d *ClientIDMetadataDocument) {
			d.TokenEndpointAuthMethod = "client_secret_post"
		}, true},
		{"grant_types with authorization_code", func(d *ClientIDMetadataDocument) {
			d.GrantTypes = []string{"authorization_code", "refresh_token"}
		}, false},
		{"grant_types without authorization_code", func(d *ClientIDMetadataDocument) {
			d.GrantTypes = []string{"client_credentials"}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := validCIMDDocument()
			tt.mutate(&doc)
			err := validateCIMDDocument(&doc, testCIMDClientID)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// --- cimdCacheTTL ---

func TestCIMDCacheTTL(t *testing.T) {
	tests := []struct {
		name         string
		cacheControl string
		configured   time.Duration
		want         time.Duration
	}{
		{"no header", "", time.Hour, time.Hour},
		{"max-age shorter", "max-age=300", time.Hour, 5 * time.Minute},
		{"max-age longer", "max-age=86400", time.Hour, time.Hour},
		{"max-age zero", "max-age=0", time.Hour, 0},
		{"unparsable", "public", time.Hour, time.Hour},
		{"no-store", "no-store", time.Hour, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, cimdCacheTTL(tt.cacheControl, tt.configured))
		})
	}
}

// --- resolveClient: DCR ---

func TestResolveClient_DCRTakesPrecedence(t *testing.T) {
	reg := StoreClientRegistration{
		ClientRegistration: ClientRegistration{
			ClientID:     "client1",
			RedirectURIs: []string{"https://app.example.com/callback"},
		},
		MCPServerName: "myserver",
	}
	regJSON, err := json.Marshal(reg)
	require.NoError(t, err)
	st := newMockStore(map[string]string{"oauth_client:client1": string(regJSON)})
	h := NewAuthHandler(st, config.Servers{}, WithCIMD(cimdEnabled()))

	got, err := h.resolveClient(t.Context(), "client1")
	require.NoError(t, err)
	require.Equal(t, ClientSourceDCR, got.Source)
	require.Equal(t, "myserver", got.MCPServerName)
}

func TestResolveClient_UnknownWithCIMDDisabled(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

func TestResolveClient_CorruptStoredRegistration(t *testing.T) {
	st := newMockStore(map[string]string{"oauth_client:client1": "NOT JSON"})
	h := NewAuthHandler(st, config.Servers{})

	_, err := h.resolveClient(t.Context(), "client1")
	require.Error(t, err)
}

// --- resolveClient: CIMD ---

func TestResolveClient_CIMDSuccess(t *testing.T) {
	h, st, fetches := newCIMDTestHandler(
		t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()),
	)

	got, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.NoError(t, err)
	require.Equal(t, ClientSourceCIMD, got.Source)
	require.Equal(t, testCIMDClientID, got.ClientID)
	require.Equal(t, []string{testCIMDRedirectURI}, got.RedirectURIs)
	require.Equal(t, "Example Client", got.ClientName)
	// CIMD クライアントはサーバー横断なので MCPServerName を持たない
	require.Empty(t, got.MCPServerName)
	require.EqualValues(t, 1, fetches.Load())

	_, cached := st.data[cimdClientCacheKey(testCIMDClientID)]
	require.True(t, cached, "resolved CIMD client should be cached")
}

func TestResolveClient_CIMDCacheHit(t *testing.T) {
	h, _, fetches := newCIMDTestHandler(
		t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()),
	)

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.NoError(t, err)
	got, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.NoError(t, err)

	require.Equal(t, testCIMDClientID, got.ClientID)
	require.EqualValues(t, 1, fetches.Load(), "second resolve should be served from cache")
}

func TestResolveClient_CIMDNotCachedWhenTTLIsZero(t *testing.T) {
	h, st, fetches := newCIMDTestHandler(
		t,
		cimdEnabled(),
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			_ = json.NewEncoder(w).Encode(validCIMDDocument())
		},
	)

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.NoError(t, err)
	_, cached := st.data[cimdClientCacheKey(testCIMDClientID)]
	require.False(t, cached)

	_, err = h.resolveClient(t.Context(), testCIMDClientID)
	require.NoError(t, err)
	require.EqualValues(t, 2, fetches.Load())
}

func TestResolveClient_CIMDClientIDMismatch(t *testing.T) {
	doc := validCIMDDocument()
	doc.ClientID = "https://other.example.com/meta.json"
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), cimdDocumentHandler(doc))

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

func TestResolveClient_CIMDWrongContentType(t *testing.T) {
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	})

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

func TestResolveClient_CIMDContentTypeWithParameters(t *testing.T) {
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(validCIMDDocument())
	})

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.NoError(t, err)
}

func TestResolveClient_CIMDNotFound(t *testing.T) {
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

func TestResolveClient_CIMDRedirectNotFollowed(t *testing.T) {
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			cimdDocumentHandler(validCIMDDocument())(w, r)
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusFound)
	})

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

func TestResolveClient_CIMDDocumentTooLarge(t *testing.T) {
	cfg := config.CIMDConfig{Enabled: true, MaxDocumentSize: 512}.WithDefaults()
	h, _, _ := newCIMDTestHandler(t, cfg, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		doc := validCIMDDocument()
		doc.ClientName = strings.Repeat("a", 1024)
		_ = json.NewEncoder(w).Encode(doc)
	})

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

func TestResolveClient_CIMDConfidentialAuthMethod(t *testing.T) {
	doc := validCIMDDocument()
	doc.TokenEndpointAuthMethod = "client_secret_post"
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), cimdDocumentHandler(doc))

	_, err := h.resolveClient(t.Context(), testCIMDClientID)
	require.Error(t, err)
}

// --- LoginEndpoint: CIMD ---

func TestLoginEndpoint_CIMDRedirectsUpstream(t *testing.T) {
	h, _, _ := newCIMDTestHandler(
		t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()),
	)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusFound, rw.Code)
	require.Contains(t, rw.Header().Get("Location"), "https://auth.example.com/auth")
}

func TestLoginEndpoint_CIMDRedirectURINotInDocument(t *testing.T) {
	h, _, _ := newCIMDTestHandler(
		t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()),
	)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, "https://evil.example.com/cb"),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusBadRequest, rw.Code)
}

func TestLoginEndpoint_CIMDDisabledRejectsURLClientID(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestLoginEndpoint_CIMDRejectedClientIDs(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
	}{
		{"http scheme", "http://client.example.com/meta.json"},
		{"ip host", "https://203.0.113.10/meta.json"},
		{"localhost", "https://localhost:9999/meta.json"},
		{"fragment", "https://client.example.com/meta.json#frag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _, fetches := newCIMDTestHandler(
				t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()),
			)
			rw := httptest.NewRecorder()

			h.LoginEndpoint(
				rw,
				cimdLoginRequest(t, tt.clientID, testCIMDRedirectURI),
				cimdTestServer(),
			)

			require.Equal(t, http.StatusUnauthorized, rw.Code)
			require.EqualValues(t, 0, fetches.Load(), "document must not be fetched")
		})
	}
}

func TestLoginEndpoint_CIMDOriginNotAllowed(t *testing.T) {
	cfg := config.CIMDConfig{
		Enabled:        true,
		AllowedOrigins: []string{"https://allowed.example.com"},
	}.WithDefaults()
	h, _, fetches := newCIMDTestHandler(t, cfg, cimdDocumentHandler(validCIMDDocument()))
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.EqualValues(t, 0, fetches.Load())
}

func TestLoginEndpoint_CIMDDocumentTooLarge(t *testing.T) {
	cfg := config.CIMDConfig{Enabled: true, MaxDocumentSize: 512}.WithDefaults()
	h, _, _ := newCIMDTestHandler(t, cfg, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		doc := validCIMDDocument()
		doc.ClientName = strings.Repeat("a", 1024)
		_ = json.NewEncoder(w).Encode(doc)
	})
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestLoginEndpoint_CIMDConfidentialAuthMethod(t *testing.T) {
	doc := validCIMDDocument()
	doc.TokenEndpointAuthMethod = "client_secret_post"
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), cimdDocumentHandler(doc))
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

func TestLoginEndpoint_CIMDClientIDMismatch(t *testing.T) {
	doc := validCIMDDocument()
	doc.ClientID = "https://other.example.com/meta.json"
	h, _, _ := newCIMDTestHandler(t, cimdEnabled(), cimdDocumentHandler(doc))
	rw := httptest.NewRecorder()

	h.LoginEndpoint(
		rw,
		cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI),
		cimdTestServer(),
	)

	require.Equal(t, http.StatusUnauthorized, rw.Code)
}

// CIMD クライアントは MCPServerName を持たないため、パスにサーバー名がない
// エイリアス（GET /authorize）ではサーバーを解決できない。
func TestLoginEndpoint_CIMDWithoutServerContext(t *testing.T) {
	h, _, _ := newCIMDTestHandler(
		t, cimdEnabled(), cimdDocumentHandler(validCIMDDocument()),
	)
	rw := httptest.NewRecorder()

	h.LoginEndpoint(rw, cimdLoginRequest(t, testCIMDClientID, testCIMDRedirectURI), nil)

	require.Equal(t, http.StatusNotFound, rw.Code)
}

// --- MetadataEndpoint: CIMD サポートの広告 ---

func TestMetadataEndpoint_CIMDSupported(t *testing.T) {
	h := NewAuthHandler(
		newMockStore(map[string]string{}),
		config.Servers{},
		WithCIMD(cimdEnabled()),
	)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, &config.Server{Name: "testserver"})

	require.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
	require.Equal(t, true, body["client_id_metadata_document_supported"])
}

func TestMetadataEndpoint_CIMDNotSupported(t *testing.T) {
	h := NewAuthHandler(newMockStore(map[string]string{}), config.Servers{})
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/.well-known/oauth-authorization-server/mcp/testserver", nil)
	req.Host = "gateway.example.com"
	rw := httptest.NewRecorder()

	h.MetadataEndpoint(rw, req, &config.Server{Name: "testserver"})

	require.Equal(t, http.StatusOK, rw.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rw.Body.Bytes(), &body))
	require.NotContains(t, body, "client_id_metadata_document_supported")
}
