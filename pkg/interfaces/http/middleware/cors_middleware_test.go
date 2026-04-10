package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCorsMiddleware_OPTIONS(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// OPTIONSの場合、nextは呼ばれない
		w.WriteHeader(http.StatusTeapot)
	})
	handler := CorsMiddleware(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/api/test", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "*", rw.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "*", rw.Header().Get("Access-Control-Allow-Methods"))
	assert.Equal(t, "*", rw.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "true", rw.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCorsMiddleware_GET(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := CorsMiddleware(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/resource", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rw.Code)
	assert.Equal(t, "*", rw.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", rw.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCorsMiddleware_POST(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	})
	handler := CorsMiddleware(next)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/resource", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusCreated, rw.Code)
	assert.Equal(t, "*", rw.Header().Get("Access-Control-Allow-Origin"))
}
