package middleware

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/netinternet/remoteaddr"
	"github.com/nonchan7720/manifold/pkg/util"
)

// responseWriter is a wrapper to capture status code.
//
// Interface embedding only promotes http.ResponseWriter's own methods, not
// http.Flusher or http.Hijacker, even when the wrapped writer implements
// them. Flush, Hijack and Unwrap are implemented explicitly below so that
// long-lived streaming responses (SSE) and protocol upgrades (WebSocket)
// keep working when routed through this middleware.
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (rw *responseWriter) Unwrap() http.ResponseWriter {
	return rw.ResponseWriter
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
		log.InfoContext(ctx, "http request")
		next.ServeHTTP(rw, r)
		log.InfoContext(ctx, "http response",
			slog.Int("status", rw.status),
			slog.Duration("duration", time.Since(start)),
		)
	})
}
