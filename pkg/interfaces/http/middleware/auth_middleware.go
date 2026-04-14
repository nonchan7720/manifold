package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

func JWT(servers config.Servers, pathValueName string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			srvName := r.PathValue(pathValueName)
			_, ok := servers[srvName]
			if !ok {
				// どうせ後ろでエラーになるのでここでは何もしない
				next.ServeHTTP(w, r)
				return
			}
			tokenStr := extractBearerToken(r)
			if tokenStr == "" {
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				// リバプロがいる場合
				if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
					scheme = forwardedProto
				}
				baseURL := fmt.Sprintf("%s://%s", scheme, r.Host)
				metadataURL := fmt.Sprintf("%s/.well-known/oauth-protected-resource/mcp/%s", baseURL, srvName)
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(
					`Bearer resource_metadata="%s"`,
					metadataURL,
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

func extractBearerToken(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if strings.HasPrefix(value, "Bearer ") {
		return value
	}
	return ""
}
