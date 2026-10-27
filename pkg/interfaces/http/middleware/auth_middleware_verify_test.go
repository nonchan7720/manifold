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

const (
	verifyTestIssuer   = "https://issuer.example.com"
	verifyTestAudience = "manifold"
	verifyTestKID      = "test-kid"
)

type verifyFixture struct {
	priv     *rsa.PrivateKey
	verifier TokenVerifier
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwk, err := jwkset.NewJWKFromKey(&priv.PublicKey, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{
			KID: verifyTestKID,
			ALG: jwkset.AlgRS256,
			USE: jwkset.UseSig,
		},
	})
	require.NoError(t, err)
	body, err := json.Marshal(jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwk.Marshal()}})
	require.NoError(t, err)
	jwks := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(jwks.Close)

	v, err := identity.NewJWTVerifier(t.Context(), verifyTestIssuer, jwks.URL, verifyTestAudience,
		identity.WithRequiredExpiration())
	require.NoError(t, err)
	return &verifyFixture{priv: priv, verifier: v}
}

func (f *verifyFixture) sign(t *testing.T, key *rsa.PrivateKey, mutate func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "user-a",
		"iss": verifyTestIssuer,
		"aud": verifyTestAudience,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = verifyTestKID
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}

// serve runs a request for /mcp/{server_name} through JWT, with verification
// configured for server "verified" only. It reports the response and the
// request next saw (nil when next was not reached).
func (f *verifyFixture) serve(
	t *testing.T,
	server, bearer string,
) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	servers := config.Servers{"verified": &config.Server{}, "plain": &config.Server{}}
	var seen *http.Request
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		w.WriteHeader(http.StatusOK)
	})
	mux := http.NewServeMux()
	mux.Handle("/mcp/{server_name}", JWT(servers, "server_name", WithTokenVerifiers(
		map[string]TokenVerifier{"verified": f.verifier},
	))(next))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp/"+server, nil)
	req.Host = "gateway.example.com"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw, seen
}

func requireChallenge(t *testing.T, rw *httptest.ResponseRecorder, seen *http.Request) {
	t.Helper()
	require.Equal(t, http.StatusUnauthorized, rw.Code)
	require.Equal(
		t,
		`Bearer resource_metadata="http://gateway.example.com/.well-known/oauth-protected-resource"`,
		rw.Header().Get("WWW-Authenticate"),
	)
	require.Nil(t, seen, "request must not reach the backend")
}

func TestJWT_Verify_ValidTokenPassesWithClaims(t *testing.T) {
	f := newVerifyFixture(t)
	token := f.sign(t, f.priv, nil)

	rw, seen := f.serve(t, "verified", token)
	require.Equal(t, http.StatusOK, rw.Code)
	require.NotNil(t, seen)
	require.Equal(t, token, contexts.FromRequestAuthHeader(seen.Context()))
	require.Equal(t, "user-a", contexts.FromTokenClaims(seen.Context())["sub"])
}

func TestJWT_Verify_Rejected(t *testing.T) {
	f := newVerifyFixture(t)
	attacker, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tests := []struct {
		name  string
		token string
	}{
		{"missing token", ""},
		{"not a jwt", "opaque-token"},
		{"bad signature", f.sign(t, attacker, nil)},
		{"wrong audience", f.sign(t, f.priv, func(c jwt.MapClaims) { c["aud"] = "other" })},
		{
			"wrong issuer",
			f.sign(t, f.priv, func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com" }),
		},
		{"expired", f.sign(t, f.priv, func(c jwt.MapClaims) {
			c["exp"] = time.Now().Add(-time.Hour).Unix()
		})},
		{"no exp", f.sign(t, f.priv, func(c jwt.MapClaims) { delete(c, "exp") })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw, seen := f.serve(t, "verified", tt.token)
			requireChallenge(t, rw, seen)
		})
	}
}

func TestJWT_Verify_UnconfiguredServerPassesThrough(t *testing.T) {
	f := newVerifyFixture(t)

	// 検証を設定していないサーバーは、署名が不正でも存在確認のみで素通しする。
	rw, seen := f.serve(t, "plain", "opaque-token")
	require.Equal(t, http.StatusOK, rw.Code)
	require.NotNil(t, seen)
	require.Equal(t, "opaque-token", contexts.FromRequestAuthHeader(seen.Context()))
	require.Nil(t, contexts.FromTokenClaims(seen.Context()))

	// トークンが無いのは従来どおり 401。
	rw, seen = f.serve(t, "plain", "")
	requireChallenge(t, rw, seen)
}
