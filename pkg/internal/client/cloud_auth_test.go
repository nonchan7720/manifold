package client

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func newServiceAccountJSON(t *testing.T) string {
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
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	require.NoError(t, err)
	return string(raw)
}

type fakeTokenEndpoint struct {
	*httptest.Server
	calls     atomic.Int32
	lastForm  url.Values
	expiresIn int
}

func newFakeTokenEndpoint(t *testing.T, expiresIn int) *fakeTokenEndpoint {
	t.Helper()
	f := &fakeTokenEndpoint{expiresIn: expiresIn}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := f.calls.Add(1)
		_ = r.ParseForm()
		f.lastForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("ya29.token-%d", n),
			"token_type":   "Bearer",
			"expires_in":   f.expiresIn,
		})
	}))
	t.Cleanup(f.Close)
	return f
}

func newBackend(t *testing.T) (*httptest.Server, *atomic.Value) {
	t.Helper()
	var last atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last.Store(r.Header.Clone())
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

func TestGoogleServiceAccountAuth_InjectsBearerToken(t *testing.T) {
	t.Setenv("TEST", "true") // HTTPClient() が httptest (127.0.0.1) を許可するために必要
	tokenSrv := newFakeTokenEndpoint(t, 3600)
	backend, last := newBackend(t)

	auth := NewGoogleServiceAccountAuth(&config.GoogleServiceAccount{
		CredentialsJSON: newServiceAccountJSON(t),
		Scopes:          []string{"https://www.googleapis.com/auth/analytics.readonly"},
	})
	auth.tokenURL = tokenSrv.URL
	c := &http.Client{Transport: auth.Wrap(http.DefaultTransport)}

	for range 3 {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)
		require.NoError(t, err)
		// 下流の Authorization は上書きされる
		req.Header.Set("Authorization", "Bearer downstream")
		resp, err := c.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "Bearer ya29.token-1", last.Load().(http.Header).Get("Authorization"))
	}
	require.EqualValues(t, 1, tokenSrv.calls.Load(), "token must be cached until it expires")
	require.Equal(
		t,
		"urn:ietf:params:oauth:grant-type:jwt-bearer",
		tokenSrv.lastForm.Get("grant_type"),
	)
	require.NotEmpty(t, tokenSrv.lastForm.Get("assertion"))
}

func TestGoogleServiceAccountAuth_RefreshesExpiredToken(t *testing.T) {
	t.Setenv("TEST", "true")
	tokenSrv := newFakeTokenEndpoint(t, 1) // 1 秒は oauth2 の期限前更新の余裕（10 秒）より短い
	backend, last := newBackend(t)

	auth := NewGoogleServiceAccountAuth(&config.GoogleServiceAccount{
		CredentialsJSON: newServiceAccountJSON(t),
		Scopes:          []string{"scope"},
	})
	auth.tokenURL = tokenSrv.URL
	c := &http.Client{Transport: auth.Wrap(http.DefaultTransport)}

	for range 2 {
		resp, err := c.Get(backend.URL) //nolint:noctx
		require.NoError(t, err)
		_ = resp.Body.Close()
	}
	require.EqualValues(t, 2, tokenSrv.calls.Load())
	require.Equal(t, "Bearer ya29.token-2", last.Load().(http.Header).Get("Authorization"))
}

func TestGoogleServiceAccountAuth_InvalidCredentials_Returns502(t *testing.T) {
	backend, _ := newBackend(t)
	auth := NewGoogleServiceAccountAuth(&config.GoogleServiceAccount{
		CredentialsJSON: "not json",
		Scopes:          []string{"scope"},
	})
	c := &http.Client{Transport: auth.Wrap(http.DefaultTransport)}
	resp, err := c.Get(backend.URL) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestGoogleServiceAccountAuth_TokenEndpointError_Returns502(t *testing.T) {
	t.Setenv("TEST", "true")
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
	}))
	t.Cleanup(tokenSrv.Close)
	backend, _ := newBackend(t)
	auth := NewGoogleServiceAccountAuth(&config.GoogleServiceAccount{
		CredentialsJSON: newServiceAccountJSON(t),
		Scopes:          []string{"scope"},
	})
	auth.tokenURL = tokenSrv.URL
	c := &http.Client{Transport: auth.Wrap(http.DefaultTransport)}
	resp, err := c.Get(backend.URL) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}

func TestNewCloudAuth(t *testing.T) {
	require.Nil(t, NewCloudAuth(nil, nil))
	require.IsType(
		t,
		&GoogleServiceAccountAuth{},
		NewCloudAuth(&config.GoogleServiceAccount{}, nil),
	)
	require.IsType(t, &AWSIAMAuth{}, NewCloudAuth(nil, &config.AWSIAM{}))
}

func TestAWSIAMAuth_SignsRequestWithSigV4(t *testing.T) {
	var gotHeader http.Header
	var gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
	}))
	t.Cleanup(backend.Close)

	auth := NewAWSIAMAuth(&config.AWSIAM{Region: "us-east-1", Service: "execute-api"})
	auth.creds = credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", "session-token")
	auth.now = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	c := &http.Client{Transport: auth.Wrap(http.DefaultTransport)}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, backend.URL+"/path",
		strings.NewReader(`{"a":1}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer downstream")
	resp, err := c.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()

	require.Equal(t, `{"a":1}`, gotBody, "body must still reach the backend after hashing")
	authz := gotHeader.Get("Authorization")
	require.True(
		t,
		strings.HasPrefix(
			authz,
			"AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20260102/us-east-1/execute-api/aws4_request",
		),
		authz,
	)
	require.Equal(t, "20260102T030405Z", gotHeader.Get("X-Amz-Date"))
	require.Equal(t, "session-token", gotHeader.Get("X-Amz-Security-Token"))
}

func TestAWSIAMAuth_CredentialError_Returns502(t *testing.T) {
	backend, _ := newBackend(t)
	auth := NewAWSIAMAuth(&config.AWSIAM{Region: "us-east-1", Service: "execute-api"})
	auth.creds = aws.CredentialsProviderFunc(func(_ context.Context) (aws.Credentials, error) {
		return aws.Credentials{}, io.ErrUnexpectedEOF
	})
	c := &http.Client{Transport: auth.Wrap(http.DefaultTransport)}
	resp, err := c.Get(backend.URL) //nolint:noctx
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusBadGateway, resp.StatusCode)
}
