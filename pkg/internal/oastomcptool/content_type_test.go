package oastomcptool

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

func TestIsTextContentType(t *testing.T) {
	tests := map[string]bool{
		"text/plain":                      true,
		"text/html; charset=utf-8":        true,
		"application/json":                true,
		"Application/JSON; charset=utf-8": true,
		"application/problem+json":        true,
		"application/xml":                 true,
		"application/atom+xml":            true,
		"application/yaml":                true,
		"application/x-yaml":              true,
		"application/yml":                 true,
		"image/png":                       false,
		"application/octet-stream":        false,
		"application/pdf":                 false,
		"":                                false,
	}
	for ct, want := range tests {
		require.Equal(t, want, IsTextContentType(ct), ct)
	}
}

func TestShouldBase64EncodeResponse(t *testing.T) {
	require.True(t, shouldBase64EncodeResponse(true, 200, "image/png"))
	require.True(t, shouldBase64EncodeResponse(true, 200, ""))
	require.False(t, shouldBase64EncodeResponse(false, 200, "image/png"))
	require.False(t, shouldBase64EncodeResponse(true, 404, "image/png"))
	require.False(t, shouldBase64EncodeResponse(true, 304, ""))
	require.False(t, shouldBase64EncodeResponse(true, 302, "image/png"))
	require.False(t, shouldBase64EncodeResponse(true, 202, "application/json"))
}

// --- CreateToolFunction（バイナリ応答の base64 化） ---

func TestCreateToolFunction_BinaryResponse_Encoding(t *testing.T) {
	png := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	tests := []struct {
		name        string
		status      int
		contentType string
		body        []byte
		wantBase64  bool
	}{
		{"404 JSON error is raw", 404, "application/json", []byte(`{"error":"not found"}`), false},
		{"500 text/plain is raw", 500, "text/plain; charset=utf-8", []byte("boom"), false},
		{"200 image/png is base64", 200, "image/png", png, true},
		{
			"202 JSON status is raw", 202, "application/json",
			[]byte(`{"status":"processing"}`), false,
		},
		{"200 empty content type is base64", 200, "", png, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType == "" {
					// 自動判定による Content-Type 付与を抑止する
					w.Header()["Content-Type"] = nil
				} else {
					w.Header().Set("Content-Type", tt.contentType)
				}
				w.WriteHeader(tt.status)
				w.Write(tt.body) //nolint: errcheck
			}
			srv := httptest.NewServer(http.HandlerFunc(handler))
			defer srv.Close()

			op := &openapi3.Operation{}
			fn := CreateToolFunction(http.DefaultClient, "/file", "get", op, srv.URL, nil, true)
			body, ct, err := fn(context.Background(), map[string]any{})
			require.NoError(t, err)
			require.Equal(t, tt.contentType, ct)
			if tt.wantBase64 {
				require.Equal(t, base64.URLEncoding.EncodeToString(tt.body), string(body))
			} else {
				require.Equal(t, tt.body, body)
			}
		})
	}
}
