package identity

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

// JWTVerifier verifies a Bearer JWT against a JWKS: signature, issuer,
// audience (when configured) and the time claims. It backs both source: jwt
// identity profiles and the optional downstream verification of
// /mcp/{server_name} (mcpServers.<name>.auth.jwt). The JWKS is cached and
// refreshed in the background by keyfunc, so a single verifier should be
// built once and shared across requests.
type JWTVerifier struct {
	issuer     string
	audience   string
	requireExp bool
	kf         keyfunc.Keyfunc
}

// JWTVerifierOption customizes NewJWTVerifier.
type JWTVerifierOption func(*JWTVerifier)

// WithRequiredExpiration rejects tokens that carry no exp claim. By default a
// token without exp is accepted (an exp that is present is always enforced).
func WithRequiredExpiration() JWTVerifierOption {
	return func(v *JWTVerifier) { v.requireExp = true }
}

// jwtResolver derives an identityKey from a Bearer JWT that Manifold itself
// verifies (issuer + JWKS signature, audience when configured) — unlike the
// existing /mcp JWT middleware, which only passes the token through for the
// backend to verify unless mcpServers.<name>.auth.jwt is set. Reverse has no
// backend to hand the token to, so Manifold is the verification endpoint here.
type jwtResolver struct {
	profile  string
	claim    string
	verifier *JWTVerifier
}

// allowedJWTAlgorithms restricts accepted signatures to the asymmetric
// algorithms a JWKS can express. HS* is deliberately excluded: it is
// symmetric, so a JWKS (public keys only) can never legitimately back it.
var allowedJWTAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
	"EdDSA",
}

// NewJWTVerifier fetches the JWKS at jwksURL and returns a verifier for
// tokens issued by issuer. audience is checked only when non-empty. It fails
// when the JWKS is unreachable at construction time rather than retrying in
// the background.
func NewJWTVerifier(
	ctx context.Context,
	issuer, jwksURL, audience string,
	opts ...JWTVerifierOption,
) (_ *JWTVerifier, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/NewJWTVerifier")
	defer func() { trace.EndSpan(ctx, rErr) }()

	failFast := false // fail here instead of retrying JWKS fetch in the background when unreachable
	kf, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{jwksURL}, keyfunc.Override{
		Client:                    client.HTTPClient(),
		NoErrorReturnFirstHTTPReq: &failFast,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS from %q: %w", jwksURL, err)
	}
	v := &JWTVerifier{issuer: issuer, audience: audience, kf: kf}
	for _, opt := range opts {
		opt(v)
	}
	return v, nil
}

// Verify parses and validates tokenString, returning its claims. Any failure
// wraps ErrUnauthenticated.
func (v *JWTVerifier) Verify(
	ctx context.Context,
	tokenString string,
) (_ jwt.MapClaims, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/JWTVerifier/Verify")
	defer func() { trace.EndSpan(ctx, rErr) }()

	opts := []jwt.ParserOption{jwt.WithIssuer(v.issuer), jwt.WithValidMethods(allowedJWTAlgorithms)}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}
	if v.requireExp {
		opts = append(opts, jwt.WithExpirationRequired())
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		v.kf.KeyfuncCtx(ctx),
		opts...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}
	return claims, nil
}

func newJWTResolver(
	ctx context.Context,
	profileName string,
	p *config.IdentityProfile,
) (_ *jwtResolver, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/newJWTResolver")
	defer func() { trace.EndSpan(ctx, rErr) }()

	verifier, err := NewJWTVerifier(ctx, p.Issuer, p.JWKSURL, p.Audience)
	if err != nil {
		return nil, fmt.Errorf("identity: profile %q: %w", profileName, err)
	}
	return &jwtResolver{
		profile:  profileName,
		claim:    p.ClaimOrDefault(),
		verifier: verifier,
	}, nil
}

func (r *jwtResolver) Resolve(
	ctx context.Context,
	req *http.Request,
) (_ domainedge.IdentityKey, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/jwtResolver/Resolve")
	defer func() { trace.EndSpan(ctx, rErr) }()

	tokenString, ok := bearerToken(req)
	if !ok {
		return "", ErrUnauthenticated
	}

	claims, err := r.verifier.Verify(ctx, tokenString)
	if err != nil {
		return "", err
	}

	value, _ := claims[r.claim].(string)
	if value == "" {
		return "", fmt.Errorf("%w: claim %q missing or empty", ErrUnauthenticated, r.claim)
	}
	return encodeIdentityKey(r.profile, value), nil
}

func bearerToken(req *http.Request) (string, bool) {
	h := req.Header.Get("Authorization")
	scheme, token, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") { // RFC 7235: scheme is case-insensitive
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
