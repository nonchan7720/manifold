package identity

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

// jwtResolver derives an identityKey from a Bearer JWT that Manifold itself
// verifies (issuer + JWKS signature, audience when configured) — unlike the
// existing /mcp JWT middleware, which only passes the token through for the
// backend to verify. Reverse has no backend to hand the token to, so
// Manifold is the verification endpoint here.
type jwtResolver struct {
	profile  string
	claim    string
	issuer   string
	audience string
	kf       keyfunc.Keyfunc
}

func newJWTResolver(
	ctx context.Context,
	profileName string,
	p *config.IdentityProfile,
) (*jwtResolver, error) {
	failFast := false // fail here instead of retrying JWKS fetch in the background when unreachable
	kf, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{p.JWKSURL}, keyfunc.Override{
		Client:                    client.HTTPClient(),
		NoErrorReturnFirstHTTPReq: &failFast,
	})
	if err != nil {
		return nil, fmt.Errorf(
			"identity: profile %q: fetch JWKS from %q: %w", profileName, p.JWKSURL, err,
		)
	}
	return &jwtResolver{
		profile:  profileName,
		claim:    p.ClaimOrDefault(),
		issuer:   p.Issuer,
		audience: p.Audience,
		kf:       kf,
	}, nil
}

func (r *jwtResolver) Resolve(
	ctx context.Context,
	req *http.Request,
) (domainedge.IdentityKey, error) {
	tokenString, ok := bearerToken(req)
	if !ok {
		return "", ErrUnauthenticated
	}

	opts := []jwt.ParserOption{jwt.WithIssuer(r.issuer)}
	if r.audience != "" {
		opts = append(opts, jwt.WithAudience(r.audience))
	}

	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(
		tokenString,
		claims,
		r.kf.KeyfuncCtx(ctx),
		opts...); err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnauthenticated, err)
	}

	value, _ := claims[r.claim].(string)
	if value == "" {
		return "", fmt.Errorf("%w: claim %q missing or empty", ErrUnauthenticated, r.claim)
	}
	return encodeIdentityKey(r.profile, value), nil
}

func bearerToken(req *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := req.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(h, prefix))
	return token, token != ""
}
