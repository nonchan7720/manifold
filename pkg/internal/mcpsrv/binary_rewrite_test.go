package mcpsrv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
)

const binaryOAS3Spec = `{
  "openapi": "3.0.0",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/single": {"get": {"operationId": "single", "responses": {"200": {"description": "ok",
      "content": {"application/json": {"schema": {"type": "object", "properties": {
        "name": {"type": "string"}, "file": {"type": "string", "format": "binary"}}}}}}}}},
    "/multi": {"get": {"operationId": "multi", "responses": {"200": {"description": "ok",
      "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Multi"}}}}}}},
    "/raw": {"get": {"operationId": "raw", "responses": {"200": {"description": "ok",
      "content": {"image/png": {"schema": {"type": "string", "format": "binary"}}}}}}}
  },
  "components": {"schemas": {
    "Scan": {"type": "object", "properties": {"scan": {"type": "string", "format": "binary"}}},
    "Multi": {"type": "object", "properties": {
      "avatar": {"type": "string", "format": "binary"},
      "doc": {"allOf": [{"$ref": "#/components/schemas/Scan"},
        {"type": "object", "properties": {"pages": {"type": "array",
          "items": {"type": "string", "format": "binary"}}}}]},
      "thumbs": {"type": "array", "items": {"type": "object", "properties": {
        "img": {"type": "string", "format": "binary"}}}},
      "title": {"type": "string"}}}
  }}
}`

const binarySwaggerSpec = `{
  "swagger": "2.0",
  "info": {"title": "t", "version": "1"},
  "produces": ["application/json"],
  "paths": {
    "/single": {"get": {"operationId": "single", "responses": {"200": {"description": "ok",
      "schema": {"type": "object", "properties": {
        "name": {"type": "string"}, "file": {"type": "string", "format": "binary"}}}}}}},
    "/multi": {"get": {"operationId": "multi", "responses": {"200": {"description": "ok",
      "schema": {"$ref": "#/definitions/Multi"}}}}}
  },
  "definitions": {
    "Scan": {"type": "object", "properties": {"scan": {"type": "file"}}},
    "Multi": {"type": "object", "properties": {
      "avatar": {"type": "string", "format": "binary"},
      "doc": {"allOf": [{"$ref": "#/definitions/Scan"},
        {"type": "object", "properties": {"pages": {"type": "array",
          "items": {"type": "string", "format": "binary"}}}}]},
      "thumbs": {"type": "array", "items": {"type": "object", "properties": {
        "img": {"type": "string", "format": "binary"}}}},
      "title": {"type": "string"}}}
  }
}`

// recordingMedia は SaveContent の呼び出しを記録し、連番の URL を返すテスト用メディアサービス。
type recordingMedia struct {
	fakeMediaService
	mu    sync.Mutex
	saved [][]byte
	types []string
}

func newRecordingMedia(enabled bool) *recordingMedia {
	m := &recordingMedia{}
	m.enabled = enabled
	m.doFunc = func(ctx context.Context, data []byte, contentType string) (string, string, error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.saved = append(m.saved, data)
		m.types = append(m.types, contentType)
		id := "id-" + strconv.Itoa(len(m.saved))
		return id, "https://media.example.com/" + id, nil
	}
	return m
}

// SaveContent は本番の ContentManagementService 同様、URL セーフ base64 をデコードしてから記録する。
func (m *recordingMedia) SaveContent(
	ctx context.Context, data []byte, contentType string,
) (string, string, error) {
	if raw, err := base64.URLEncoding.DecodeString(string(data)); err == nil {
		data = raw
	}
	return m.doFunc(ctx, data, contentType)
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func callBinaryTool(
	t *testing.T,
	spec, tool string,
	upstream http.HandlerFunc,
	media storage.MediaService,
) *mcp.CallToolResult {
	t.Helper()
	ts := httptest.NewServer(upstream)
	t.Cleanup(ts.Close)

	path := filepath.Join(t.TempDir(), "spec.json")
	require.NoError(t, os.WriteFile(path, []byte(spec), 0o600))
	reg, err := RegisterOpenAPI(t.Context(), path, ts.URL, nil)
	require.NoError(t, err)

	srv := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "0"}, nil)
	attachTools(srv, reg, media)
	ct, st := mcp.NewInMemoryTransports()
	_, err = srv.Connect(t.Context(), st, nil)
	require.NoError(t, err)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "0"}, nil).
		Connect(t.Context(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: tool, Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%+v", res.Content)
	return res
}

func jsonResponder(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func resourceLinks(res *mcp.CallToolResult) []*mcp.ResourceLink {
	var out []*mcp.ResourceLink
	for _, c := range res.Content {
		if l, ok := c.(*mcp.ResourceLink); ok {
			out = append(out, l)
		}
	}
	return out
}

func TestBinaryFieldsInJSONResponse(t *testing.T) {
	multiBody := `{"avatar":"` + b64("AVATAR") + `","title":"hello",` +
		`"doc":{"scan":"` + b64("SCAN") + `","pages":["` + b64("P1") + `","` + b64("P2") + `"]},` +
		`"thumbs":[{"img":"` + b64("T1") + `"},{"img":null},{"img":"` + b64("T2") + `"}]}`

	for _, format := range []struct{ name, spec string }{
		{"openapi3", binaryOAS3Spec},
		{"swagger2", binarySwaggerSpec},
	} {
		t.Run(format.name+"/single field", func(t *testing.T) {
			media := newRecordingMedia(true)
			res := callBinaryTool(t, format.spec, "single",
				jsonResponder(`{"name":"n","file":"`+b64("hello-bytes")+`"}`), media)
			got := structured(t, res)
			require.Equal(t, "n", got["name"])
			require.Equal(t, "https://media.example.com/id-1", got["file"])
			require.Equal(t, [][]byte{[]byte("hello-bytes")}, media.saved)
			require.Len(t, resourceLinks(res), 1)
			require.Equal(t, "https://media.example.com/id-1", resourceLinks(res)[0].URI)
			// TextContent にも書き換え後の JSON が入る
			require.Contains(t, res.Content[0].(*mcp.TextContent).Text, "id-1")
		})

		t.Run(format.name+"/multiple, nested and array fields", func(t *testing.T) {
			media := newRecordingMedia(true)
			res := callBinaryTool(t, format.spec, "multi", jsonResponder(multiBody), media)
			got := structured(t, res)
			require.Equal(t, "hello", got["title"])
			require.Len(t, media.saved, 6)
			require.Len(t, resourceLinks(res), 6)

			urls := map[string]bool{}
			for _, l := range resourceLinks(res) {
				urls[l.URI] = true
			}
			require.Len(t, urls, 6, "各ファイルは別々の URL になる")

			doc := got["doc"].(map[string]any)
			require.True(t, urls[got["avatar"].(string)])
			require.True(t, urls[doc["scan"].(string)])
			for _, p := range doc["pages"].([]any) {
				require.True(t, urls[p.(string)])
			}
			thumbs := got["thumbs"].([]any)
			require.True(t, urls[thumbs[0].(map[string]any)["img"].(string)])
			require.Nil(t, thumbs[1].(map[string]any)["img"])
			require.True(t, urls[thumbs[2].(map[string]any)["img"].(string)])
		})

		t.Run(format.name+"/media disabled leaves JSON unchanged", func(t *testing.T) {
			res := callBinaryTool(t, format.spec, "multi", jsonResponder(multiBody),
				storage.NewNoopUploader())
			require.Empty(t, resourceLinks(res))
			require.JSONEq(t, multiBody, res.Content[0].(*mcp.TextContent).Text)
		})

		t.Run(format.name+"/invalid base64 left as is", func(t *testing.T) {
			media := newRecordingMedia(true)
			res := callBinaryTool(t, format.spec, "single",
				jsonResponder(`{"name":"n","file":"not base64 !!"}`), media)
			require.Equal(t, "not base64 !!", structured(t, res)["file"])
			require.Empty(t, media.saved)
			require.Empty(t, resourceLinks(res))
		})
	}
}

// 本文全体が format: binary のレスポンスは従来どおり resource_link になる。
func TestBinaryWholeBodyResponse_StillResourceLink(t *testing.T) {
	media := newRecordingMedia(true)
	png := "\x89PNG\r\n\x1a\n0000000000000000"
	res := callBinaryTool(t, binaryOAS3Spec, "raw", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte(png))
	}, media)
	links := resourceLinks(res)
	require.Len(t, links, 1)
	require.Equal(t, "https://media.example.com/id-1", links[0].URI)
	require.Equal(t, [][]byte{[]byte(png)}, media.saved)
}

func TestRewriteBinaryFields_Base64Variants(t *testing.T) {
	raw := []byte{0xfb, 0xff, 0xfe, 0x01} // 標準/URL セーフで文字が異なるバイト列
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"std padded", base64.StdEncoding.EncodeToString(raw), true},
		{"url padded", base64.URLEncoding.EncodeToString(raw), true},
		{"std raw", base64.RawStdEncoding.EncodeToString(raw), true},
		{"url raw", base64.RawURLEncoding.EncodeToString(raw), true},
		{"invalid", "%%%", false},
		{"empty", "", false},
	}
	fields := []oastomcptool.BinaryField{{Path: []string{"f"}}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := newRecordingMedia(true)
			body, links, err := rewriteBinaryFields(
				t.Context(), []byte(`{"f":"`+tt.value+`"}`), fields, media)
			require.NoError(t, err)
			if tt.ok {
				require.Equal(t, [][]byte{raw}, media.saved)
				require.Len(t, links, 1)
				require.JSONEq(t, `{"f":"https://media.example.com/id-1"}`, string(body))
			} else {
				require.Empty(t, media.saved)
				require.Empty(t, links)
			}
		})
	}
}

// contentMediaType があれば Content-Type のヒントに使い、無ければ実体から判定する。
func TestRewriteBinaryFields_ContentType(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n0000000000000000"
	tests := []struct {
		name  string
		field oastomcptool.BinaryField
		data  string
		want  string
	}{
		{
			"hint",
			oastomcptool.BinaryField{Path: []string{"f"}, MediaType: "application/pdf"},
			"x",
			"application/pdf",
		},
		{"sniffed", oastomcptool.BinaryField{Path: []string{"f"}}, png, "image/png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := newRecordingMedia(true)
			_, links, err := rewriteBinaryFields(t.Context(),
				[]byte(`{"f":"`+b64(tt.data)+`"}`), []oastomcptool.BinaryField{tt.field}, media)
			require.NoError(t, err)
			require.Equal(t, []string{tt.want}, media.types)
			require.Equal(t, tt.want, links[0].(*mcp.ResourceLink).MIMEType)
		})
	}
}

// 配列レスポンス（wrapToolFunc で {"items": [...]} に包まれる）内のバイナリも置き換える。
func TestRewriteBinaryFields_WrappedArray(t *testing.T) {
	media := newRecordingMedia(true)
	fields := []oastomcptool.BinaryField{{Path: []string{oastomcptool.BinaryArrayItem, "img"}}}
	body, links, err := rewriteBinaryFields(t.Context(),
		[]byte(`{"items":[{"img":"`+b64("a")+`"},{"img":"`+b64("b")+`"}]}`), fields, media)
	require.NoError(t, err)
	require.Len(t, links, 2)
	require.JSONEq(
		t,
		`{"items":[{"img":"https://media.example.com/id-1"},{"img":"https://media.example.com/id-2"}]}`,
		string(body),
	)
}

// generated catalog（tools.file）から構築したツールでも、spec から導出した
// バイナリフィールドの位置が登録され、JSON 内の binary が URL に置き換わる。
func TestBinaryFields_GeneratedCatalog(t *testing.T) {
	specPath := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(specPath, []byte(binaryOAS3Spec), 0o600))
	g := newGeneratedCatalogFromSpec(t, specPath)
	catPath := filepath.Join(t.TempDir(), "tools.yaml")
	f, err := os.Create(catPath)
	require.NoError(t, err)
	require.NoError(t, oastomcptool.WriteGeneratedCatalog(f, g))
	require.NoError(t, f.Close())

	reg, err := RegisterOpenAPI(
		t.Context(), "unused", "http://backend", nil, WithGeneratedToolsFile(catPath),
	)
	require.NoError(t, err)
	var found bool
	for _, tool := range reg.ListTools() {
		if tool.tool.Name == "single" {
			found = true
			require.Equal(t, [][]string{{"file"}}, paths(tool.binaryFields))
		}
	}
	require.True(t, found)
}

func paths(fields []oastomcptool.BinaryField) [][]string {
	var out [][]string
	for _, f := range fields {
		out = append(out, f.Path)
	}
	return out
}
