package extract

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeMIME(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n")
	for _, tc := range []struct {
		name     string
		declared string
		data     []byte
		want     string
	}{
		{name: "declared", declared: "Application/PDF", data: png, want: "application/pdf"},
		{name: "parameters are dropped", declared: "text/plain; charset=utf-8", want: "text/plain"},
		{name: "empty is sniffed", data: png, want: "image/png"},
		{name: "octet-stream is sniffed", declared: "application/octet-stream", data: png, want: "image/png"},
		{name: "malformed is sniffed", declared: ";;", data: []byte("%PDF-1.7"), want: "application/pdf"},
		{name: "unknown bytes", data: []byte{0x00, 0x01}, want: "application/octet-stream"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, NormalizeMIME(tc.declared, tc.data))
		})
	}
}

func TestIsText(t *testing.T) {
	require.True(t, IsText("text/markdown", []byte("# 見出し")))
	require.True(t, IsText("application/json", []byte(`{}`)))
	require.True(t, IsText("application/ld+json", []byte(`{}`)))
	require.True(t, IsText("image/svg+xml", []byte(`<svg/>`)))
	require.False(t, IsText("application/pdf", []byte("%PDF-1.7")))
	// UTF-8 として不正なものはテキストとして扱わない。
	require.False(t, IsText("text/plain", []byte{0xff, 0xfe}))
}

func TestMatchMIME(t *testing.T) {
	patterns := []string{"application/pdf", " Image/* "}
	require.True(t, MatchMIME(patterns, "application/pdf"))
	require.True(t, MatchMIME(patterns, "image/tiff"))
	require.False(t, MatchMIME(patterns, "audio/mpeg"))
	require.True(t, MatchMIME([]string{"*/*"}, "audio/mpeg"))
	require.False(t, MatchMIME(nil, "application/pdf"))
}

func TestCleanOutput(t *testing.T) {
	require.Empty(t, CleanOutput("  "+NoTextMarker+"\n"))
	require.Equal(t, "hello", CleanOutput(" hello\n"))
}
