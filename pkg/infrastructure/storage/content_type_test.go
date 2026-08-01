package storage

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	pngHeader  = []byte("\x89PNG\r\n\x1a\n0000000000000000")
	jpegHeader = []byte("\xff\xd8\xff\xe0" + "0000000000000000")
	gifHeader  = []byte("GIF89a" + "0000000000000000")
	pdfHeader  = []byte("%PDF-1.7\n0000000000000000")
)

func TestResolveContentType_UnknownDeclared_SniffsFromBody(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		data     []byte
		want     string
	}{
		{name: "octet-stream の PNG", declared: "application/octet-stream", data: pngHeader, want: "image/png"},
		{name: "octet-stream の JPEG", declared: "application/octet-stream", data: jpegHeader, want: "image/jpeg"},
		{name: "octet-stream の GIF", declared: "application/octet-stream", data: gifHeader, want: "image/gif"},
		{name: "octet-stream の PDF", declared: "application/octet-stream", data: pdfHeader, want: "application/pdf"},
		{name: "Content-Type 未指定の PNG", declared: "", data: pngHeader, want: "image/png"},
		{name: "パラメータ付き octet-stream", declared: "application/octet-stream; charset=binary", data: pngHeader, want: "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResolveContentType(tt.declared, tt.data))
		})
	}
}

func TestResolveContentType_DecodesBase64BeforeSniffing(t *testing.T) {
	// generateContent に渡るバイナリは base64 のままなので、そのままスニッフすると
	// text/plain と判定されてしまう。SaveContent と同じ規則でデコードしてから判定する。
	encoded := []byte(base64.URLEncoding.EncodeToString(pngHeader))

	require.Equal(t, "image/png", ResolveContentType("application/octet-stream", encoded))
}

func TestResolveContentType_KeepsMeaningfulDeclaredType(t *testing.T) {
	// 上流が型を返しているなら、それが正。実体からの推測で上書きしない。
	require.Equal(t, "image/png", ResolveContentType("image/png", pdfHeader))
	require.Equal(t, "application/json", ResolveContentType("application/json", []byte(`{"a":1}`)))
	require.Equal(t, "text/csv; charset=utf-8", ResolveContentType("text/csv; charset=utf-8", []byte("a,b\n1,2")))
}

func TestResolveContentType_UndetectableStaysOctetStream(t *testing.T) {
	// 判定できないものは octet-stream のまま返す（誤った型を名乗らせない）。
	require.Equal(t, "application/octet-stream", ResolveContentType("application/octet-stream", []byte{0x00, 0x01, 0x02, 0x03}))
}

func TestResolveContentType_EmptyBodyKeepsDeclared(t *testing.T) {
	require.Equal(t, "application/octet-stream", ResolveContentType("application/octet-stream", nil))
}
