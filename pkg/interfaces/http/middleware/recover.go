package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
)

func Recover(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		*r = *r.WithContext(ctx)
		defer func() {
			if rvr := recover(); rvr != nil {
				stack := debug.Stack()
				slog.ErrorContext(ctx, "panic recovered",
					slog.Any("panic", rvr),
					slog.String("stack", string(stack)),
				)
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
