package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaxBodyBytes(t *testing.T) {
	// 本文を読み、MaxBytesReader のエラーを 413 に変換する下流ハンドラー。
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	do := func(limit int64, body string, chunked bool) int {
		req := httptest.NewRequestWithContext(
			t.Context(), http.MethodPost, "/mcp/x", strings.NewReader(body),
		)
		if chunked {
			req.ContentLength = -1
		}
		rec := httptest.NewRecorder()
		MaxBodyBytes(limit)(next).ServeHTTP(rec, req)
		return rec.Code
	}

	require.Equal(t, http.StatusOK, do(10, "0123456789", false), "at the limit")
	require.Equal(
		t,
		http.StatusRequestEntityTooLarge,
		do(10, "0123456789a", false),
		"Content-Length over limit",
	)
	require.Equal(
		t,
		http.StatusRequestEntityTooLarge,
		do(10, "0123456789a", true),
		"unknown length over limit",
	)
	require.Equal(t, http.StatusOK, do(0, strings.Repeat("a", 1<<20), false), "0 means unlimited")
	require.Equal(
		t,
		http.StatusOK,
		do(-1, strings.Repeat("a", 1<<20), false),
		"negative means unlimited",
	)
}
