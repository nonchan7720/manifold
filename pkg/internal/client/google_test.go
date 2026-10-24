package client

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
	"sync/atomic"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

const testGoogleScope = "https://www.googleapis.com/auth/analytics.readonly"

// fakeServiceAccount は生成した RSA 鍵と、token_uri を tokenURI にしたサービス
// アカウントキー（JSON）を返す。
func fakeServiceAccount(t *testing.T, tokenURI string) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   "sa@example.iam.gserviceaccount.com",
		"private_key_id": "kid",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      tokenURI,
	})
	require.NoError(t, err)
	return key, string(raw)
}

func TestGoogleRoundTripper_InjectsBearerAndCachesToken(t *testing.T) {
	t.Setenv("TEST", "true") // HTTPClient() が httptest (127.0.0.1) を許可するために必要

	var tokenCalls atomic.Int32
	var key *rsa.PrivateKey
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		require.NoError(t, r.ParseForm())
		require.Equal(
			t,
			"urn:ietf:params:oauth:grant-type:jwt-bearer",
			r.PostForm.Get("grant_type"),
		)
		// アサーションは偽キーで署名され、要求したスコープとサービスアカウントを持つ。
		claims := jwt.MapClaims{}
		_, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims,
			func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
			jwt.WithValidMethods([]string{"RS256"}),
		)
		require.NoError(t, err)
		require.Equal(t, testGoogleScope, claims["scope"])
		require.Equal(t, "sa@example.iam.gserviceaccount.com", claims["iss"])

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "google-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)
	var saJSON string
	key, saJSON = fakeServiceAccount(t, tokenSrv.URL)

	var gotAuth []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
	}))
	t.Cleanup(backend.Close)

	source := NewGoogleTokenSource(&config.GoogleServiceAccount{
		CredentialsJSON: saJSON,
		Scopes:          []string{testGoogleScope},
	})
	c := &http.Client{Transport: NewGoogleRoundTripper(http.DefaultTransport, source)}

	for range 2 {
		// 呼び出し元の Authorization は使わず、サービスアカウントのトークンで置き換える。
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer downstream-token")
		resp, err := c.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	require.Equal(t, []string{"Bearer google-access-token", "Bearer google-access-token"}, gotAuth)
	require.EqualValues(t, 1, tokenCalls.Load(), "token must be cached across requests")
}

func TestGoogleRoundTripper_TokenErrorFailsRequest(t *testing.T) {
	t.Setenv("TEST", "true")

	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	t.Cleanup(tokenSrv.Close)
	_, saJSON := fakeServiceAccount(t, tokenSrv.URL)

	var backendCalled atomic.Bool
	backend := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		backendCalled.Store(true)
	}))
	t.Cleanup(backend.Close)

	source := NewGoogleTokenSource(&config.GoogleServiceAccount{
		CredentialsJSON: saJSON,
		Scopes:          []string{testGoogleScope},
	})
	c := &http.Client{Transport: NewGoogleRoundTripper(http.DefaultTransport, source)}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)
	require.NoError(t, err)
	resp, err := c.Do(req) //nolint:bodyclose // エラー時はレスポンスが無い
	require.Error(t, err)
	require.Nil(t, resp)
	require.Contains(t, err.Error(), "google service account token")
	require.False(t, backendCalled.Load(), "backend must not be called without a token")
}

func TestGoogleTokenSource_InvalidCredentials(t *testing.T) {
	source := NewGoogleTokenSource(&config.GoogleServiceAccount{
		CredentialsJSON: "{",
		Scopes:          []string{testGoogleScope},
	})
	_, err := source.Token()
	require.ErrorContains(t, err, "invalid service account credentials")
	// 失敗は固定され、リクエストのたびに同じエラーを返す。
	_, err = source.Token()
	require.ErrorContains(t, err, "invalid service account credentials")
}

func TestGoogleTokenSource_ADC(t *testing.T) {
	t.Setenv("TEST", "true")

	var tokenCalls atomic.Int32
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "adc-access-token", "token_type": "Bearer", "expires_in": 3600,
		})
	}))
	t.Cleanup(tokenSrv.Close)
	_, saJSON := fakeServiceAccount(t, tokenSrv.URL)
	credFile := filepath.Join(t.TempDir(), "adc.json")
	require.NoError(t, os.WriteFile(credFile, []byte(saJSON), 0o600))
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credFile)

	var gotAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
	}))
	t.Cleanup(backend.Close)

	// 資格情報の解決は最初のリクエストまで遅延する。
	source := NewGoogleTokenSource(&config.GoogleServiceAccount{Scopes: []string{testGoogleScope}})
	require.EqualValues(t, 0, tokenCalls.Load())
	c := &http.Client{Transport: NewGoogleRoundTripper(http.DefaultTransport, source)}
	for range 2 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)
		require.NoError(t, err)
		resp, err := c.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}
	require.Equal(t, "Bearer adc-access-token", gotAuth)
	require.EqualValues(t, 1, tokenCalls.Load(), "token must be cached across requests")
}

func TestGoogleTokenSource_ADC_NotFound(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "none.json"))
	source := NewGoogleTokenSource(&config.GoogleServiceAccount{Scopes: []string{testGoogleScope}})
	_, err := source.Token()
	require.ErrorContains(t, err, "google application default credentials")
}
