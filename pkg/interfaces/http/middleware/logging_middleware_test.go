package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func TestLogging_OK(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := Logging(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/test/path", nil)
	req.Header.Set("X-Request-Id", "req-123")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	require.True(t, called)
	require.Equal(t, http.StatusOK, rw.Code)
}

func TestLogging_NotFound(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	handler := Logging(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	require.Equal(t, http.StatusNotFound, rw.Code)
}

func TestLogging_WithUserAgent(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := Logging(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/resource", nil)
	req.Header.Set("User-Agent", "TestAgent/1.0\ninjection")
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	require.Equal(t, http.StatusOK, rw.Code)
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	rw := httptest.NewRecorder()
	wrapper := &responseWriter{ResponseWriter: rw, status: http.StatusOK}

	wrapper.WriteHeader(http.StatusNotFound)

	require.Equal(t, http.StatusNotFound, wrapper.status)
	require.Equal(t, http.StatusNotFound, rw.Code)
}

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rw := httptest.NewRecorder()
	wrapper := &responseWriter{ResponseWriter: rw, status: http.StatusOK}

	// WriteHeaderを呼ばない場合、statusはデフォルト200
	require.Equal(t, http.StatusOK, wrapper.status)
}

func TestResponseWriter_Flush_DelegatesToUnderlyingFlusher(t *testing.T) {
	rw := httptest.NewRecorder()
	wrapper := &responseWriter{ResponseWriter: rw, status: http.StatusOK}

	wrapper.Flush()

	require.True(t, rw.Flushed)
}

// plainResponseWriter implements only http.ResponseWriter, to exercise the
// responseWriter branches where the underlying writer supports neither
// http.Flusher nor http.Hijacker.
type plainResponseWriter struct {
	header http.Header
	code   int
}

func (w *plainResponseWriter) Header() http.Header         { return w.header }
func (w *plainResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *plainResponseWriter) WriteHeader(code int)        { w.code = code }

func TestResponseWriter_Flush_NoopWhenUnderlyingNotFlusher(t *testing.T) {
	wrapper := &responseWriter{
		ResponseWriter: &plainResponseWriter{header: http.Header{}},
		status:         http.StatusOK,
	}

	require.NotPanics(t, func() { wrapper.Flush() })
}

func TestResponseWriter_Hijack_ReturnsErrNotSupportedWhenUnderlyingNotHijacker(t *testing.T) {
	wrapper := &responseWriter{
		ResponseWriter: &plainResponseWriter{header: http.Header{}},
		status:         http.StatusOK,
	}

	_, _, err := wrapper.Hijack()

	require.ErrorIs(t, err, http.ErrNotSupported)
}

func TestResponseWriter_Unwrap_ReturnsUnderlying(t *testing.T) {
	rw := httptest.NewRecorder()
	wrapper := &responseWriter{ResponseWriter: rw, status: http.StatusOK}

	require.Same(t, http.ResponseWriter(rw), wrapper.Unwrap())
}

// TestLogging_SSEStreaming_FlushesBeforeHandlerReturns guards against the
// regression where a long-lived SSE stream (e.g. MCP's subscriptions/listen)
// hangs at 0 bytes because Logging's responseWriter never forwards
// http.Flusher, so the client never observes data flushed before the
// handler returns.
func TestLogging_SSEStreaming_FlushesBeforeHandlerReturns(t *testing.T) {
	const chunk = "data: hello\n\n"
	release := make(chan struct{})
	defer close(release)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, chunk)
		f, ok := w.(http.Flusher)
		require.True(t, ok, "responseWriter must forward http.Flusher")
		f.Flush()
		<-release
	})

	srv := httptest.NewServer(Logging(next))
	defer srv.Close()

	// bodyclose linter false positive: the goroutine below reading resp.Body
	// defeats its escape analysis, but the defer on the next line does close
	// it on every path, including t.Fatal (which still runs deferred calls).
	resp, err := http.Get(srv.URL) //nolint:noctx,bodyclose
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	readDone := make(chan []byte, 1)
	go func() {
		buf := make([]byte, len(chunk))
		n, _ := io.ReadFull(resp.Body, buf)
		readDone <- buf[:n]
	}()

	select {
	case got := <-readDone:
		require.Equal(t, chunk, string(got))
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive flushed data before handler returned; Flush was not forwarded")
	}
}

// TestLogging_WebSocketUpgrade_HijackSucceeds guards against the regression
// where Logging's responseWriter never forwards http.Hijacker, so any route
// behind it (e.g. /edge/ws) could never hijack the connection to upgrade to
// WebSocket.
func TestLogging_WebSocketUpgrade_HijackSucceeds(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		require.NoError(t, err)
		conn.Close(websocket.StatusNormalClosure, "")
	})

	srv := httptest.NewServer(Logging(next))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, resp, err := websocket.Dial(t.Context(), wsURL, nil)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close() //nolint:errcheck
	}
	require.NoError(t, err, "Logging should still allow hijacking the connection")
	conn.CloseNow()
}
