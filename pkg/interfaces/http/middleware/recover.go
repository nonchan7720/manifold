package middleware

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/n-creativesystem/go-packages/lib/trace"
)

func Recover(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = trace.StartSpan(ctx, "Middleware/Recover")
		var spanErr error
		defer func() { trace.EndSpan(ctx, spanErr) }()
		*r = *r.WithContext(ctx)
		defer func() {
			if rvr := recover(); rvr != nil {
				stack := debug.Stack()
				slog.ErrorContext(ctx, "panic recovered",
					slog.Any("panic", rvr),
					slog.String("stack", string(stack)),
				)
				spanErr = fmt.Errorf("panic: %v", rvr)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{ //nolint: errcheck
					"error": "Internal server error",
				})
			}
		}()
		h.ServeHTTP(w, r)
	})
}
