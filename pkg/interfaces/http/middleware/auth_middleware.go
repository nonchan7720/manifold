package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

func JWT(servers config.Servers, pathValueName string) func(http.Handler) http.Handler {
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
			*r = *r.WithContext(ctx)
			requireBearer(w, r, next)
		})
	}
}

// RequireBearer is JWT for a route with no {server_name}, such as the
// aggregated /mcp endpoint: it requires a bearer token and passes it on to
// the backends through the request context.
func RequireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := trace.StartSpan(r.Context(), "Middleware/RequireBearer")
		defer func() { trace.EndSpan(ctx, nil) }()
		*r = *r.WithContext(ctx)
		requireBearer(w, r, next)
	})
}

// requireBearer answers 401 (with the RFC 9728 resource metadata challenge)
// when r carries no bearer token, and otherwise stores the token in r's
// context for the backends and calls next. r is updated in place, so the
// outer middlewares see the token too.
func requireBearer(w http.ResponseWriter, r *http.Request, next http.Handler) {
	tokenStr := extractBearerToken(r)
	if tokenStr == "" {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(
			`Bearer resource_metadata="%s"`,
			ProtectedResourceMetadataURL(r),
		))
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	*r = *r.WithContext(contexts.ToRequestAuthHeader(r.Context(), tokenStr))
	next.ServeHTTP(w, r)
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
