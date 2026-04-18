package middleware

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

func MCPServerApp(servers config.Servers, pathValueName string) func(next http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			ctx = trace.StartSpan(ctx, "Middleware/MCPServer")
			defer func() { trace.EndSpan(ctx, nil) }()

			srvName := r.PathValue(pathValueName)
			v, ok := servers[srvName]
			if !ok {
				http.NotFound(w, r)
				return
			}
			headerPrefix := fmt.Sprintf("x-%s-", v.Name)
			header := map[string][]string{}
			for key, value := range r.Header {
				if after, found := strings.CutPrefix(key, headerPrefix); found {
					header[after] = value
				}
			}
			ctx = contexts.ToServerContext(ctx, v)
			ctx = contexts.ToHeaderContext(ctx, header)
			*r = *r.WithContext(ctx)
			next(w, r)
		}
	}
}
