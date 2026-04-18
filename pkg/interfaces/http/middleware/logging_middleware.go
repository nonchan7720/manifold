package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/netinternet/remoteaddr"
	"github.com/nonchan7720/manifold/pkg/util"
)

// responseWriter is a wrapper to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// clientIP is the shared remoteaddr parser for extracting real client IPs
// behind proxies (Cloudflare, OCI LB, Traefik, etc.).
var clientIP = remoteaddr.Parse()

// Logging returns a middleware that logs HTTP requests.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = trace.StartSpan(ctx, "Middleware/Logging")
		defer func() { trace.EndSpan(ctx, nil) }()
		*r = *r.WithContext(ctx)

		start := time.Now()
		ip, _ := clientIP.IP(r)
		log := slog.With(
			slog.String("method", r.Method),
			slog.String("path", util.SanitizeLog(r.URL.Path)),
			slog.String("ip", util.SanitizeLog(ip)),
			slog.String("user_agent", util.SanitizeLog(r.UserAgent())),
			slog.String("request_id", r.Header.Get("X-Request-Id")),
			slog.String("host", r.Host),
			slog.String("request-uri", util.SanitizeLog(r.RequestURI)),
		)
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		log.Info("http request")
		next.ServeHTTP(rw, r)
		log.Info("http response",
			slog.Int("status", rw.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}
