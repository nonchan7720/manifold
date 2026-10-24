package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// TokenVerifier verifies a downstream Bearer token and returns its claims.
// identity.JWTVerifier satisfies it.
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (jwt.MapClaims, error)
}

// JWTOption customizes JWT.
type JWTOption func(*jwtOptions)

type jwtOptions struct {
	verifiers map[string]TokenVerifier
}

// WithTokenVerifiers makes JWT verify the Bearer token of the servers named in
// verifiers (mcpServers.<name>.auth.jwt) instead of only requiring its
// presence. Servers without an entry keep the pass-through behavior.
func WithTokenVerifiers(verifiers map[string]TokenVerifier) JWTOption {
	return func(o *jwtOptions) {
		o.verifiers = verifiers
	}
}

// JWT requires a Bearer token on the request to a configured server. By
// default the token is only checked for presence and passed through for the
// backend to verify; a server with a verifier (WithTokenVerifiers) has it
// verified here, and a token that fails is rejected with 401.
func JWT(
	servers config.Servers,
	pathValueName string,
	opts ...JWTOption,
) func(http.Handler) http.Handler {
	var o jwtOptions
	for _, opt := range opts {
		opt(&o)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = trace.StartSpan(ctx, "Middleware/JWT")
			defer func() { trace.EndSpan(ctx, nil) }()

			srvName := r.PathValue(pathValueName)
			_, ok := servers[srvName]
			if !ok {
				// どうせ後ろでエラーになるのでここでは何もしない
				next.ServeHTTP(w, r)
				return
			}
			tokenStr := extractBearerToken(r)
			if tokenStr == "" {
				unauthorized(w, r)
				return
			}
			if verifier, ok := o.verifiers[srvName]; ok {
				claims, err := verifier.Verify(ctx, tokenStr)
				if err != nil {
					slog.WarnContext(ctx, "JWT: downstream token verification failed",
						slog.String("server", srvName), slog.String("error", err.Error()))
					unauthorized(w, r)
					return
				}
				ctx = contexts.ToTokenClaims(ctx, claims)
			}
			ctx = contexts.ToRequestAuthHeader(ctx, tokenStr)
			*r = *r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
}

func unauthorized(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(
		`Bearer resource_metadata="%s"`,
		ProtectedResourceMetadataURL(r),
	))
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// ProtectedResourceMetadataURL builds the RFC 9728 resource metadata URL for
// r's host/scheme, used as the WWW-Authenticate challenge's
// resource_metadata parameter (RFC 6750) on a 401.
func ProtectedResourceMetadataURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// リバプロがいる場合
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	}
	return fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", scheme, r.Host)
}

func extractBearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if token, ok := strings.CutPrefix(value, "Bearer "); ok {
		return token
	}
	return ""
}
