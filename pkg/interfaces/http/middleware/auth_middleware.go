package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// TokenVerifier verifies a downstream Bearer token (mcpServers.<name>.auth.jwt).
type TokenVerifier interface {
	Verify(ctx context.Context, token string) (map[string]any, error)
}

// JWT is the /mcp/{server_name} auth middleware. By default it only requires a
// Bearer token to be present and passes it through to the backend. A server
// with an entry in verifiers (auth.jwt) additionally has the token verified
// here, and a token that fails verification gets the same 401 challenge as a
// missing one.
func JWT(
	servers config.Servers,
	pathValueName string,
	verifiers map[string]TokenVerifier,
) func(http.Handler) http.Handler {
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
			if tokenStr != "" {
				if v, ok := verifiers[srvName]; ok {
					if _, err := v.Verify(ctx, tokenStr); err != nil {
						slog.WarnContext(ctx, "JWT verification failed",
							slog.String("server", srvName), slog.String("error", err.Error()))
						tokenStr = ""
					}
				}
			}
			if tokenStr == "" {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(
					`Bearer resource_metadata="%s"`,
					ProtectedResourceMetadataURL(r),
				))
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			ctx = contexts.ToRequestAuthHeader(ctx, tokenStr)
			*r = *r.WithContext(ctx)
			next.ServeHTTP(w, r)
		})
	}
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
