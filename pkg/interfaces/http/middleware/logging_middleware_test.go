package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
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

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestLogging_NotFound(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	handler := Logging(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/missing", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusNotFound, rw.Code)
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

	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestResponseWriter_WriteHeader(t *testing.T) {
	rw := httptest.NewRecorder()
	wrapper := &responseWriter{ResponseWriter: rw, status: http.StatusOK}

	wrapper.WriteHeader(http.StatusNotFound)

	assert.Equal(t, http.StatusNotFound, wrapper.status)
	assert.Equal(t, http.StatusNotFound, rw.Code)
}

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rw := httptest.NewRecorder()
	wrapper := &responseWriter{ResponseWriter: rw, status: http.StatusOK}

	// WriteHeaderを呼ばない場合、statusはデフォルト200
	assert.Equal(t, http.StatusOK, wrapper.status)
}
