package identity

import (
	"context"
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
	"github.com/stretchr/testify/require"
)

func newTestJWKSServer(t *testing.T, kid string, pub *rsa.PublicKey) *httptest.Server {
	t.Helper()
	jwk, err := jwkset.NewJWKFromKey(pub, jwkset.JWKOptions{
		Metadata: jwkset.JWKMetadataOptions{KID: kid, ALG: jwkset.AlgRS256, USE: jwkset.UseSig},
	})
	require.NoError(t, err)
	marshaled := jwkset.JWKSMarshal{Keys: []jwkset.JWKMarshal{jwk.Marshal()}}
	body, err := json.Marshal(marshaled)
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func signRSAToken(t *testing.T, priv *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(priv)
	require.NoError(t, err)
	return signed
}

func newBearerRequest(token string) *http.Request {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp/app1", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func newJWTClaims(sub, issuer string) jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"sub": sub,
		"iss": issuer,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
}

// jwtTestFixture bundles an RSA key pair, a JWKS server serving its public
// key, and a matching identity profile, so each test only states what it
// varies.
type jwtTestFixture struct {
	priv    *rsa.PrivateKey
	kid     string
	issuer  string
	profile *config.IdentityProfile
}

func newJWTTestFixture(t *testing.T) *jwtTestFixture {
	t.Helper()
	t.Setenv("TEST", "true") // client.HTTPClient() が httptest (127.0.0.1) を許可するために必要

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	const kid = "test-kid"
	const issuer = "https://idp.example.com"
	srv := newTestJWKSServer(t, kid, &priv.PublicKey)

	return &jwtTestFixture{
		priv:   priv,
		kid:    kid,
		issuer: issuer,
		profile: &config.IdentityProfile{
			Source:  config.IdentitySourceJWT,
			Issuer:  issuer,
			JWKSURL: srv.URL,
		},
	}
}

func (f *jwtTestFixture) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	return signRSAToken(t, f.priv, f.kid, claims)
}

func TestJWTResolver_Resolve_ValidToken_ReturnsIdentityKey(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	token := f.sign(t, newJWTClaims("user-a", f.issuer))
	key, err := r.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("oauth", "user-a"), key)
}

func TestJWTResolver_Resolve_TokenRotation_SameSub_SameIdentityKey(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	token1 := f.sign(t, newJWTClaims("user-a", f.issuer))
	key1, err := r.Resolve(t.Context(), newBearerRequest(token1))
	require.NoError(t, err)

	claims2 := newJWTClaims("user-a", f.issuer)
	claims2["iat"] = claims2["iat"].(int64) + 1
	claims2["exp"] = claims2["exp"].(int64) + 1
	token2 := f.sign(t, claims2)
	key2, err := r.Resolve(t.Context(), newBearerRequest(token2))
	require.NoError(t, err)

	require.Equal(
		t,
		key1,
		key2,
		"a freshly issued token for the same sub must resolve to the same identityKey",
	)
}

func TestJWTResolver_Resolve_NoAuthorizationHeader_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	_, err = r.Resolve(t.Context(), newBearerRequest(""))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_MalformedAuthorizationHeader_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp/app1", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	_, err = r.Resolve(t.Context(), req)
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_InvalidSignature_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	attackerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	// 公開されている kid を騙るが、実際には JWKS に無い鍵で署名する。
	token := signRSAToken(t, attackerKey, f.kid, newJWTClaims("user-a", f.issuer))
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_WrongIssuer_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	token := f.sign(t, newJWTClaims("user-a", "https://attacker.example.com"))
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_ExpiredToken_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	claims := newJWTClaims("user-a", f.issuer)
	claims["exp"] = time.Now().Add(-time.Hour).Unix()
	token := f.sign(t, claims)
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_MissingClaim_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	claims := newJWTClaims("user-a", f.issuer)
	delete(claims, "sub")
	token := f.sign(t, claims)
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_EmptyClaim_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	token := f.sign(t, newJWTClaims("", f.issuer))
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_CustomClaim_ExtractsConfiguredClaim(t *testing.T) {
	f := newJWTTestFixture(t)
	f.profile.Claim = "email"
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	claims := newJWTClaims("user-a", f.issuer)
	claims["email"] = "user-a@example.com"
	token := f.sign(t, claims)
	key, err := r.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("oauth", "user-a@example.com"), key)
}

func TestJWTResolver_Resolve_AudienceConfigured_Mismatch_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	f.profile.Audience = "manifold"
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	claims := newJWTClaims("user-a", f.issuer)
	claims["aud"] = "other-service"
	token := f.sign(t, claims)
	_, err = r.Resolve(t.Context(), newBearerRequest(token))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_AudienceConfigured_Match_Valid(t *testing.T) {
	f := newJWTTestFixture(t)
	f.profile.Audience = "manifold"
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	claims := newJWTClaims("user-a", f.issuer)
	claims["aud"] = "manifold"
	token := f.sign(t, claims)
	key, err := r.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("oauth", "user-a"), key)
}

func TestJWTResolver_Resolve_AudienceNotConfigured_AnyAudienceAccepted(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	claims := newJWTClaims("user-a", f.issuer)
	claims["aud"] = "whatever"
	token := f.sign(t, claims)
	key, err := r.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("oauth", "user-a"), key)
}

func TestJWTResolver_Resolve_LowercaseBearerScheme_Valid(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	token := f.sign(t, newJWTClaims("user-a", f.issuer))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp/app1", nil)
	// RFC 7235: scheme comparison is case-insensitive.
	req.Header.Set("Authorization", "bearer "+token)
	key, err := r.Resolve(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("oauth", "user-a"), key)
}

func TestBearerToken_SchemeCaseInsensitive(t *testing.T) {
	for _, scheme := range []string{"Bearer", "bearer", "BEARER", "BeArEr"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp/app1", nil)
		req.Header.Set("Authorization", scheme+" some-token")
		token, ok := bearerToken(req)
		require.True(t, ok, "scheme %q must be accepted", scheme)
		require.Equal(t, "some-token", token)
	}
}

func TestJWTResolver_Resolve_DisallowedAlgorithm_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	// HS256 is symmetric and not among the JWKS-backed asymmetric algorithms
	// this resolver allows.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, newJWTClaims("user-a", f.issuer))
	signed, err := token.SignedString([]byte("some-shared-secret"))
	require.NoError(t, err)

	_, err = r.Resolve(t.Context(), newBearerRequest(signed))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_NoneAlgorithm_ErrUnauthenticated(t *testing.T) {
	f := newJWTTestFixture(t)
	r, err := NewResolver(t.Context(), "oauth", f.profile, nil)
	require.NoError(t, err)

	token := jwt.NewWithClaims(jwt.SigningMethodNone, newJWTClaims("user-a", f.issuer))
	signed, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
	require.NoError(t, err)

	_, err = r.Resolve(t.Context(), newBearerRequest(signed))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestJWTResolver_Resolve_ProfileIsolation_SameSubDifferentProfile_DifferentIdentityKey(
	t *testing.T,
) {
	f := newJWTTestFixture(t)
	rA, err := NewResolver(t.Context(), "profileA", f.profile, nil)
	require.NoError(t, err)
	rB, err := NewResolver(t.Context(), "profileB", f.profile, nil)
	require.NoError(t, err)

	token := f.sign(t, newJWTClaims("user-a", f.issuer))
	keyA, err := rA.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)
	keyB, err := rB.Resolve(t.Context(), newBearerRequest(token))
	require.NoError(t, err)
	require.NotEqual(t, keyA, keyB)
}
