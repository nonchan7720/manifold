package storage

import (
	"archive/zip"
	"bytes"
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

// docxBytes は docx として判定される最小の ZIP（[Content_Types].xml と word/document.xml を含む）
// を生成する。http.DetectContentType では application/zip にしかならない形式の代表例。
// paddingSize > 0 のときは word/ より前に無圧縮の docProps/app.xml を挟み、word/ エントリの
// 位置を先頭から paddingSize バイト以上後ろへずらす。
func docxBytes(t *testing.T, paddingSize int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, method uint16, body []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		require.NoError(t, err)
		_, err = w.Write(body)
		require.NoError(t, err)
	}
	// mimetype は先頭エントリが [Content_Types].xml 等でなければ Office 文書とみなさない。
	write("[Content_Types].xml", zip.Deflate, []byte(
		`<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`,
	))
	if paddingSize > 0 {
		write("docProps/app.xml", zip.Store, bytes.Repeat([]byte("x"), paddingSize))
	}
	write("word/document.xml", zip.Deflate, []byte(
		`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"></w:document>`,
	))
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestResolveContentType_UnknownDeclared_SniffsFromBody(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		data     []byte
		want     string
	}{
		{
			name:     "octet-stream の PNG",
			declared: "application/octet-stream",
			data:     pngHeader,
			want:     "image/png",
		},
		{
			name:     "octet-stream の JPEG",
			declared: "application/octet-stream",
			data:     jpegHeader,
			want:     "image/jpeg",
		},
		{
			name:     "octet-stream の GIF",
			declared: "application/octet-stream",
			data:     gifHeader,
			want:     "image/gif",
		},
		{
			name:     "octet-stream の PDF",
			declared: "application/octet-stream",
			data:     pdfHeader,
			want:     "application/pdf",
		},
		{
			name:     "先頭に改行が付いた PDF",
			declared: "application/octet-stream",
			data:     append([]byte("\n"), pdfHeader...),
			want:     "application/pdf",
		},
		{
			name:     "UTF-8 BOM 付きの PDF",
			declared: "application/octet-stream",
			data:     append([]byte("\xef\xbb\xbf"), pdfHeader...),
			want:     "application/pdf",
		},
		{name: "Content-Type 未指定の PNG", declared: "", data: pngHeader, want: "image/png"},
		{
			name:     "パラメータ付き octet-stream",
			declared: "application/octet-stream; charset=binary",
			data:     pngHeader,
			want:     "image/png",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ResolveContentType(tt.declared, tt.data))
		})
	}
}

func TestResolveContentType_DetectsOfficeDocument(t *testing.T) {
	// ZIP ベースの Office 文書は http.DetectContentType だと application/zip にしかならない。
	// mimetype なら docx として判定できる。
	const docx = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

	t.Run("word/ が先頭近くにある", func(t *testing.T) {
		require.Equal(t, docx, ResolveContentType("application/octet-stream", docxBytes(t, 0)))
	})
	t.Run("word/ が mimetype 既定の 4096 バイトより後ろにある", func(t *testing.T) {
		// 先行エントリが大きい docx でも、detectLimit まで読むので application/zip に落ちない。
		data := docxBytes(t, 8<<10)
		require.Greater(t, bytes.Index(data, []byte("word/document.xml")), 4096)
		require.Equal(t, docx, ResolveContentType("application/octet-stream", data))
	})
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
	require.Equal(
		t,
		"text/csv; charset=utf-8",
		ResolveContentType("text/csv; charset=utf-8", []byte("a,b\n1,2")),
	)
}

func TestResolveContentType_UndetectableStaysOctetStream(t *testing.T) {
	// 判定できないものは octet-stream のまま返す（誤った型を名乗らせない）。
	require.Equal(
		t,
		"application/octet-stream",
		ResolveContentType("application/octet-stream", []byte{0x00, 0x01, 0x02, 0x03}),
	)
}

func TestResolveContentType_EmptyBodyKeepsDeclared(t *testing.T) {
	require.Equal(
		t,
		"application/octet-stream",
		ResolveContentType("application/octet-stream", nil),
	)
}
