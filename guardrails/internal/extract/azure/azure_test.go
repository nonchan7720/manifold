package azure

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
	"github.com/stretchr/testify/require"
)

// azureServer は Responses API の代わりに、リクエストを記録して body を返す。
func azureServer(t *testing.T, status int, body string) (*Extractor, *request, *http.Header) {
	t.Helper()
	var got request
	var header http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Clone()
		if r.URL.Path != "/openai/v1/responses" || r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "secret-key", "my-deployment", 4096, srv.Client()), &got, &header
}

const completed = `{"status":"completed","output":[
  {"type":"reasoning","content":[]},
  {"type":"message","content":[{"type":"output_text","text":"hello "},{"type":"output_text","text":"world"}]}
]}`

func TestAzureExtractor_PDF(t *testing.T) {
	e, got, header := azureServer(t, http.StatusOK, completed)
	require.True(t, e.Supports("application/pdf"))
	require.False(t, e.Supports("application/msword"))

	text, err := e.Extract(t.Context(), extract.SourceFile{
		URI: "file:///secret-name.pdf", MIMEType: "application/pdf", Data: []byte("%PDF"),
	})
	require.NoError(t, err)
	require.Equal(t, "hello world", text)

	require.Equal(t, "secret-key", header.Get("api-key"))
	require.Equal(t, "my-deployment", got.Model)
	require.Equal(t, extract.SystemPrompt, got.Instructions)
	require.Equal(t, 4096, got.MaxOutputTokens)
	require.False(t, got.Store)
	parts := got.Input[0].Content
	require.Len(t, parts, 2)
	require.Equal(t, "input_file", parts[0].Type)
	// filename はモデルに見えるので、URI は使わない。
	require.Equal(t, "document.pdf", parts[0].Filename)
	require.Equal(t, "data:application/pdf;base64,JVBERg==", parts[0].FileData)
	require.Equal(t, part{Type: "input_text", Text: extract.UserPrompt}, parts[1])
}

func TestAzureExtractor_Image(t *testing.T) {
	e, got, _ := azureServer(t, http.StatusOK, completed)
	_, err := e.Extract(t.Context(), extract.SourceFile{MIMEType: "image/png", Data: []byte("img")})
	require.NoError(t, err)
	require.Equal(t, part{
		Type: "input_image", ImageURL: "data:image/png;base64,aW1n", Detail: "high",
	}, got.Input[0].Content[0])
}

func TestAzureExtractor_Failures(t *testing.T) {
	file := extract.SourceFile{MIMEType: "image/png", Data: []byte("img")}
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		truncated bool
		wantErr   string
	}{
		{name: "truncated", status: http.StatusOK, truncated: true,
			body: `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}`},
		{name: "content filter", status: http.StatusOK, wantErr: "incomplete",
			body: `{"status":"incomplete","incomplete_details":{"reason":"content_filter"}}`},
		{name: "failed", status: http.StatusOK, wantErr: `"failed"`, body: `{"status":"failed"}`},
		{name: "invalid json", status: http.StatusOK, wantErr: "decode response", body: `not json`},
		// 応答の本文はファイルの内容を含みうるので、エラー文には出さない。
		{name: "http error", status: http.StatusBadRequest, wantErr: "status 400",
			body: `{"error":{"message":"the file says: top secret"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := azureServer(t, tc.status, tc.body)
			_, err := e.Extract(t.Context(), file)
			require.Error(t, err)
			if tc.truncated {
				require.ErrorIs(t, err, extract.ErrTruncated)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
			require.NotContains(t, err.Error(), "top secret")
		})
	}
}
