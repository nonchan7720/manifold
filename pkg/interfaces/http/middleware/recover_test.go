package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecover_NoPanic(t *testing.T) {
	var called bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := Recover(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rw.Code)
}

func TestRecover_WithPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("something went wrong")
	})
	handler := Recover(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.Equal(t, "application/json", rw.Header().Get("Content-Type"))

	var body map[string]string
	err := json.Unmarshal(rw.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "Internal server error", body["error"])
}

func TestRecover_WithNilPanic(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(nil)
	})
	handler := Recover(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rw := httptest.NewRecorder()

	// nilパニックは runtime.PanicNilError になる場合がある（Go 1.21+）
	// どちらにせよRecoverはパニックをキャッチして500を返すべき
	assert.NotPanics(t, func() {
		handler.ServeHTTP(rw, req)
	})
}
