package mcpsrv

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// newGoogleTestServiceAccount は、アクセストークン accessToken を返すトークン
// サーバーを token_uri に持つ偽のサービスアカウント設定を返す。
func newGoogleTestServiceAccount(t *testing.T, accessToken string) *config.GoogleServiceAccount {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken, "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "sa@example.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":    tokenSrv.URL,
	})
	require.NoError(t, err)
	return &config.GoogleServiceAccount{
		CredentialsJSON: string(raw),
		Scopes:          []string{"https://www.googleapis.com/auth/analytics.readonly"},
	}
}

func TestHTTPClientRoundTripper_GoogleServiceAccount_InjectsBearer(t *testing.T) {
	var gotAuth, gotExtra string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotExtra = r.Header.Get("X-Extra")
	}))
	t.Cleanup(backend.Close)

	rt := httpClientRoundTripper(
		nil,
		nil,
		nil,
		newGoogleTestServiceAccount(t, "ga-token"),
		[]string{backend.URL},
		map[string]string{"X-Extra": "1"},
	)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: rt}).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())

	require.Equal(t, "Bearer ga-token", gotAuth)
	require.Equal(t, "1", gotExtra)
}

func TestHTTPClientRoundTripper_GoogleServiceAccount_InvalidCredentialsFailRequests(t *testing.T) {
	rt := httpClientRoundTripper(nil, nil, nil, &config.GoogleServiceAccount{
		CredentialsJSON: "{", Scopes: []string{"s"},
	}, nil, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:1", nil)
	require.NoError(t, err)
	resp, err := rt.RoundTrip(req) //nolint:bodyclose // エラー時はレスポンスが無い
	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "invalid service account credentials")
}

func TestMCPBackendClient_SharedGoogleSource(t *testing.T) {
	c := &MCPBackendClient{name: "b", cfg: &config.Server{
		Transport:            config.MCPTransportHTTP,
		URL:                  "http://example.com",
		GoogleServiceAccount: newGoogleTestServiceAccount(t, "ga-token"),
	}}
	first := c.sharedGoogleSource()
	require.NotNil(t, first)
	second := c.sharedGoogleSource()
	require.Same(
		t,
		first,
		second,
		"token source (and its token cache) must be shared across connections",
	)

	none := &MCPBackendClient{name: "n", cfg: &config.Server{}}
	require.Nil(t, none.sharedGoogleSource())
}

func TestRegisterOpenAPI_GoogleServiceAccountRequiresBaseURL(t *testing.T) {
	spec := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(spec, []byte(
		`{"openapi":"3.0.0","info":{"title":"t","version":"1"},"paths":{}}`,
	), 0o600))

	// servers も無くローカルの spec からも baseURL を導出できない場合、トークンの送信先を
	// 固定できないので登録を失敗させる。
	_, err := RegisterOpenAPI(t.Context(), spec, "", nil,
		WithGoogleServiceAccount(&config.GoogleServiceAccount{Scopes: []string{"s"}}),
	)
	require.ErrorContains(t, err, "googleServiceAccount requires a resolvable baseURL")

	// Google 認証が無ければ従来どおり登録できる。
	_, err = RegisterOpenAPI(t.Context(), spec, "", nil)
	require.NoError(t, err)
}
