package openapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nonchan7720/manifold/guardrails/internal/extract"
	"github.com/stretchr/testify/require"
)

// writeSpec は spec を一時ファイルに書いてパスを返す。
func writeSpec(t *testing.T, spec string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	require.NoError(t, os.WriteFile(path, []byte(spec), 0o600))
	return path
}

// received はサービスが受け取ったリクエスト。
type received struct {
	method, path, query string
	header              http.Header
	body                []byte
	form                map[string]string
	fileName, fileType  string
	file                []byte
}

// ocrServer はリクエストを記録し、contentType の body を返す OCR サービス。
func ocrServer(t *testing.T, status int, contentType, body string) (*httptest.Server, *received) {
	t.Helper()
	got := &received{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.query = r.Method, r.URL.Path, r.URL.RawQuery
		got.header = r.Header.Clone()
		if r.Header.Get("Content-Type") != "" && r.ParseMultipartForm(1<<20) == nil &&
			r.MultipartForm != nil {
			got.form = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				got.form[k] = v[0]
			}
			for _, headers := range r.MultipartForm.File {
				f, _ := headers[0].Open()
				got.file, _ = io.ReadAll(f)
				got.fileName = headers[0].Filename
				got.fileType = headers[0].Header.Get("Content-Type")
			}
		} else {
			got.body, _ = io.ReadAll(r.Body)
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// バイナリの本文と text/plain の応答（Apache Tika の形）。
const rawSpec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /tika/{lang}:
    parameters:
      - {name: lang, in: path, required: true, schema: {type: string}}
    put:
      operationId: extractText
      parameters:
        - {name: mode, in: query, schema: {type: string}}
        - {name: X-Source-Type, in: header, schema: {type: string}}
      requestBody:
        content:
          application/octet-stream:
            schema: {type: string, format: binary}
      responses:
        "200":
          description: text
          content:
            text/plain:
              schema: {type: string}
`

func TestExtractor_RawBodyAndTextResponse(t *testing.T) {
	srv, got := ocrServer(t, http.StatusOK, "text/plain", "  hello from tika\n")
	e, err := New(Config{
		SpecPath:    writeSpec(t, rawSpec),
		OperationID: "extractText",
		ServerURL:   srv.URL + "/",
		Headers:     map[string]string{"Authorization": "Bearer token"},
		Params: map[string]string{
			"lang":          "ja jp",
			"mode":          "fast",
			"X-Source-Type": "{mime_type}",
		},
		MIMETypes: []string{"application/pdf", "image/*"},
	}, srv.Client())
	require.NoError(t, err)
	require.True(t, e.Supports("image/tiff"))
	require.False(t, e.Supports("audio/mpeg"))

	text, err := e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")},
	)
	require.NoError(t, err)
	require.Equal(t, "hello from tika", text)

	require.Equal(t, http.MethodPut, got.method)
	require.Equal(t, "/tika/ja jp", got.path)
	require.Equal(t, "mode=fast", got.query)
	require.Equal(t, []byte("%PDF"), got.body)
	// spec が application/octet-stream だけを宣言しているので、その種類で送る。
	// ファイルの種類は、パラメータの {mime_type} で別に渡せる。
	require.Equal(t, "application/octet-stream", got.header.Get("Content-Type"))
	require.Equal(t, "application/pdf", got.header.Get("X-Source-Type"))
	require.Equal(t, "Bearer token", got.header.Get("Authorization"))
	require.Equal(t, "text/plain", got.header.Get("Accept"))
}

// multipart の本文と、配列の中にテキストがある JSON の応答。
const multipartSpec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
servers:
  - url: https://ocr.example.com/api
paths:
  /v1/ocr:
    post:
      operationId: ocr
      requestBody:
        content:
          multipart/form-data:
            schema:
              type: object
              required: [document, language]
              properties:
                document: {type: string, format: binary}
                language: {type: string}
                hint: {type: string}
          application/json:
            schema:
              type: object
              properties:
                content: {type: string, format: byte}
      responses:
        "200":
          description: pages
          content:
            application/json:
              schema:
                type: object
                properties:
                  pages:
                    type: array
                    items:
                      type: object
                      properties:
                        text: {type: string}
`

func TestExtractor_MultipartAndJSONPath(t *testing.T) {
	srv, got := ocrServer(t, http.StatusOK, "application/json",
		`{"pages":[{"text":"page one"},{"text":"page two"}]}`)
	e, err := New(Config{
		SpecPath: writeSpec(t, multipartSpec), OperationID: "ocr", ServerURL: srv.URL,
		Params: map[string]string{"language": "ja"}, TextPath: "pages.*.text",
		MIMETypes: []string{"*/*"},
	}, srv.Client())
	require.NoError(t, err)

	text, err := e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "image/png", Data: []byte("img")},
	)
	require.NoError(t, err)
	require.Equal(t, "page one\npage two", text)

	require.Equal(t, "/v1/ocr", got.path)
	require.Equal(t, map[string]string{"language": "ja"}, got.form)
	require.Equal(t, []byte("img"), got.file)
	require.Equal(t, "file.png", got.fileName)
	require.Equal(t, "image/png", got.fileType)
	require.Equal(t, "application/json", got.header.Get("Accept"))
}

// JSON の本文（base64）と、文字列のプロパティが1つだけの JSON の応答。
const jsonSpec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /transcribe:
    post:
      operationId: transcribe
      requestBody:
        content:
          application/json:
            schema:
              type: object
              properties:
                content: {type: string, format: byte}
                mimeType: {type: string}
                dpi: {type: integer}
      responses:
        "201":
          description: text
          content:
            application/json:
              schema:
                type: object
                properties:
                  text: {type: string}
                  pages: {type: integer}
`

func TestExtractor_JSONBodyInfersFields(t *testing.T) {
	srv, got := ocrServer(
		t,
		http.StatusCreated,
		"application/json",
		`{"text":"transcribed","pages":2}`,
	)
	e, err := New(Config{
		SpecPath: writeSpec(t, jsonSpec), OperationID: "transcribe", ServerURL: srv.URL,
		Params:    map[string]string{"mimeType": "{mime_type}", "dpi": "300"},
		MIMETypes: []string{"application/pdf"},
	}, srv.Client())
	require.NoError(t, err)

	text, err := e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")},
	)
	require.NoError(t, err)
	require.Equal(t, "transcribed", text)

	var body map[string]any
	require.NoError(t, json.Unmarshal(got.body, &body))
	require.Equal(t, map[string]any{
		"content":  base64.StdEncoding.EncodeToString([]byte("%PDF")),
		"mimeType": "application/pdf",
		"dpi":      float64(300), // 文字列以外のプロパティは JSON の値として入れる
	}, body)
}

func TestExtractor_ResponseFailures(t *testing.T) {
	file := extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")}
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		textPath string
		wantErr  string
	}{
		// 応答の本文はファイルの内容を含みうるので、エラー文には出さない。
		{name: "http error", status: http.StatusInternalServerError, body: `{"text":"top secret"}`,
			wantErr: "status 500"},
		{name: "invalid json", status: http.StatusCreated, body: `not json`, wantErr: "decode response"},
		// パスの誤りを「テキスト無し」として通さない。
		{name: "missing key", status: http.StatusCreated, body: `{"result":"top secret"}`,
			wantErr: `no "text" on the text path`},
		{name: "non-string leaf", status: http.StatusCreated, body: `{"text":{"a":"top secret"}}`,
			wantErr: "non-string value"},
		{name: "index out of range", status: http.StatusCreated, body: `{"pages":[]}`,
			textPath: "pages.0.text", wantErr: `no element "0"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := ocrServer(t, tc.status, "application/json", tc.body)
			e, err := New(Config{
				SpecPath: writeSpec(t, jsonSpec), OperationID: "transcribe", ServerURL: srv.URL,
				TextPath: tc.textPath, MIMETypes: []string{"application/pdf"},
			}, srv.Client())
			require.NoError(t, err)
			_, err = e.Extract(t.Context(), file)
			require.ErrorContains(t, err, tc.wantErr)
			require.NotContains(t, err.Error(), "top secret")
		})
	}

	// "*" が指す配列が空なのは、テキストが無いという正常な結果。
	srv, _ := ocrServer(t, http.StatusCreated, "application/json", `{"pages":[]}`)
	e, err := New(Config{
		SpecPath: writeSpec(t, jsonSpec), OperationID: "transcribe", ServerURL: srv.URL,
		TextPath: "pages.*.text", MIMETypes: []string{"application/pdf"},
	}, srv.Client())
	require.NoError(t, err)
	text, err := e.Extract(t.Context(), file)
	require.NoError(t, err)
	require.Empty(t, text)
}

// 呼び出し方が spec から決まらないときは、起動時にエラーにする。
func TestNew_Errors(t *testing.T) {
	const ambiguous = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
servers: [{url: "https://{tenant}.example.com"}]
paths:
  /ocr:
    post:
      operationId: ocr
      requestBody:
        content:
          multipart/form-data:
            schema:
              type: object
              properties:
                front: {type: string, format: binary}
                back: {type: string, format: binary}
      responses:
        "200":
          description: ok
          content:
            application/json:
              schema:
                type: object
                properties:
                  text: {type: string}
                  language: {type: string}
  /nobody:
    get:
      operationId: nobody
      responses:
        "200": {description: ok}
`
	path := writeSpec(t, ambiguous)
	const server = "https://ocr.example.com"
	for _, tc := range []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{name: "missing spec", cfg: Config{SpecPath: path + ".missing", OperationID: "ocr"},
			wantErr: "load spec"},
		{name: "unknown operation", cfg: Config{SpecPath: path, OperationID: "nope", ServerURL: server},
			wantErr: `operation "nope" is not in the spec`},
		{name: "server with variables", cfg: Config{SpecPath: path, OperationID: "ocr"},
			wantErr: "server URL"},
		{name: "no request body", cfg: Config{SpecPath: path, OperationID: "nobody", ServerURL: server},
			wantErr: "no request body"},
		{name: "ambiguous file field", cfg: Config{SpecPath: path, OperationID: "ocr", ServerURL: server},
			wantErr: "several candidates (back, front)"},
		{name: "unknown file field", cfg: Config{
			SpecPath: path, OperationID: "ocr", ServerURL: server, FileField: "side",
		}, wantErr: `no property "side"`},
		{name: "ambiguous text", cfg: Config{
			SpecPath: path, OperationID: "ocr", ServerURL: server, FileField: "front",
		}, wantErr: "several candidates (language, text)"},
		{name: "missing path parameter", cfg: Config{
			SpecPath: writeSpec(t, rawSpec), OperationID: "extractText", ServerURL: server,
		}, wantErr: `parameter "lang" (in path) needs a value`},
		{name: "missing required body field", cfg: Config{
			SpecPath: writeSpec(t, multipartSpec), OperationID: "ocr", TextPath: "pages.*.text",
		}, wantErr: `property "language" needs a value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.cfg, http.DefaultClient)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}

	// 両方を指定すれば決まる。spec の servers（変数無し）も使える。
	_, err := New(Config{
		SpecPath: path, OperationID: "ocr", ServerURL: server, FileField: "front", TextPath: "text",
	}, http.DefaultClient)
	require.NoError(t, err)
	_, err = New(Config{
		SpecPath: writeSpec(t, multipartSpec), OperationID: "ocr",
		Params: map[string]string{"language": "ja"}, TextPath: "pages.*.text",
	}, http.DefaultClient)
	require.NoError(t, err)
}

// spec がバイナリの本文を種類ごとに定義しているときは、ファイルの種類に合うものを選ぶ。
// spec が受け付けない種類は扱わない。
const multiRawSpec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /ocr:
    post:
      operationId: ocr
      requestBody:
        content:
          application/pdf:
            schema: {type: string, format: binary}
          image/png:
            schema: {type: string, format: binary}
      responses:
        "200":
          description: text
          content:
            text/plain:
              schema: {type: string}
`

func TestExtractor_RawBodyMatchesFileType(t *testing.T) {
	srv, got := ocrServer(t, http.StatusOK, "text/plain", "text")
	e, err := New(Config{
		SpecPath: writeSpec(t, multiRawSpec), OperationID: "ocr", ServerURL: srv.URL,
		MIMETypes: []string{"application/pdf", "image/*"},
	}, srv.Client())
	require.NoError(t, err)

	for _, mimeType := range []string{"image/png", "application/pdf"} {
		require.True(t, e.Supports(mimeType))
		_, err := e.Extract(t.Context(), extract.SourceFile{MIMEType: mimeType, Data: []byte("x")})
		require.NoError(t, err)
		require.Equal(t, mimeType, got.header.Get("Content-Type"))
	}

	// --openapi-mime-types には一致しても、spec に無い種類は送らない。
	require.False(t, e.Supports("image/tiff"))
	_, err = e.Extract(t.Context(), extract.SourceFile{MIMEType: "image/tiff", Data: []byte("x")})
	require.ErrorContains(t, err, `does not accept "image/tiff"`)
}

// ワイルドカードの本文は、ファイルの種類を Content-Type にする。octet-stream と併記
// されていても、ファイルの種類を受け付けるワイルドカードを優先する。
func TestExtractor_RawBodyWildcard(t *testing.T) {
	const spec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /ocr:
    post:
      operationId: ocr
      requestBody:
        content:
          image/*:
            schema: {type: string, format: binary}
          application/octet-stream:
            schema: {type: string, format: binary}
      responses:
        "200":
          description: text
          content:
            text/plain:
              schema: {type: string}
`
	srv, got := ocrServer(t, http.StatusOK, "text/plain", "text")
	e, err := New(Config{
		SpecPath: writeSpec(t, spec), OperationID: "ocr", ServerURL: srv.URL,
		MIMETypes: []string{"*/*"},
	}, srv.Client())
	require.NoError(t, err)
	_, err = e.Extract(t.Context(), extract.SourceFile{MIMEType: "image/tiff", Data: []byte("x")})
	require.NoError(t, err)
	require.Equal(t, "image/tiff", got.header.Get("Content-Type"))

	// ワイルドカードに合わない種類は、octet-stream として送る。
	_, err = e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "application/pdf", Data: []byte("x")},
	)
	require.NoError(t, err)
	require.Equal(t, "application/octet-stream", got.header.Get("Content-Type"))
}

// JSON の本文は、spec の種類（vendor の +json も）をそのまま Content-Type にする。
func TestExtractor_JSONBodyKeepsVendorMediaType(t *testing.T) {
	const spec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /ocr:
    post:
      operationId: ocr
      requestBody:
        content:
          application/vnd.ocr+json:
            schema:
              type: object
              properties:
                content: {type: string, format: byte}
      responses:
        "200":
          description: text
          content:
            text/plain:
              schema: {type: string}
`
	srv, got := ocrServer(t, http.StatusOK, "text/plain", "text")
	e, err := New(Config{
		SpecPath: writeSpec(t, spec), OperationID: "ocr", ServerURL: srv.URL,
		MIMETypes: []string{"application/pdf"},
	}, srv.Client())
	require.NoError(t, err)
	_, err = e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")},
	)
	require.NoError(t, err)
	require.Equal(t, "application/vnd.ocr+json", got.header.Get("Content-Type"))
	require.JSONEq(t, `{"content":"JVBERg=="}`, string(got.body))
}

// 書き起こしが済んだことを表す 200 / 201 以外は、成功として扱わない。202（受け付けた
// だけ）や 204（本文なし）を通すと、書き起こされていないファイルが「テキスト無し」になる。
func TestExtractor_RejectsIncompleteStatuses(t *testing.T) {
	file := extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")}
	for _, status := range []int{http.StatusAccepted, http.StatusNoContent, http.StatusPartialContent} {
		srv, _ := ocrServer(t, status, "text/plain", "")
		e, err := New(Config{
			SpecPath: writeSpec(t, rawSpec), OperationID: "extractText", ServerURL: srv.URL,
			Params: map[string]string{"lang": "ja"}, MIMETypes: []string{"application/pdf"},
		}, srv.Client())
		require.NoError(t, err)
		_, err = e.Extract(t.Context(), file)
		require.ErrorContainsf(t, err, "returned status", "status %d", status)
	}

	// 200 で本文が空なのは、テキストの無いファイルという正常な結果。
	srv, _ := ocrServer(t, http.StatusOK, "text/plain", "")
	e, err := New(Config{
		SpecPath: writeSpec(t, rawSpec), OperationID: "extractText", ServerURL: srv.URL,
		Params: map[string]string{"lang": "ja"}, MIMETypes: []string{"application/pdf"},
	}, srv.Client())
	require.NoError(t, err)
	text, err := e.Extract(t.Context(), file)
	require.NoError(t, err)
	require.Empty(t, text)
}

// サービスの URL（query パラメータの API キーなど）を、呼び出しの失敗のエラーに残さない。
func TestExtractor_CallErrorOmitsURL(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	serverURL := closed.URL
	closed.Close()

	e, err := New(Config{
		SpecPath: writeSpec(t, rawSpec), OperationID: "extractText", ServerURL: serverURL,
		Params:    map[string]string{"lang": "ja", "mode": "api-key-secret"},
		MIMETypes: []string{"application/pdf"},
	}, http.DefaultClient)
	require.NoError(t, err)
	_, err = e.Extract(
		t.Context(),
		extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")},
	)
	require.ErrorContains(t, err, "openapi: call: request failed")
	require.NotContains(t, err.Error(), "secret")
	require.NotContains(t, err.Error(), serverURL)
}

// 200 と 201 で本文の形式が違う operation（200 は JSON、201 は text/plain）。
const mixedStatusSpec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /ocr:
    post:
      operationId: ocr
      requestBody:
        content:
          application/octet-stream:
            schema: {type: string, format: binary}
      responses:
        "200":
          description: text
          content:
            application/json:
              schema:
                type: object
                properties:
                  text: {type: string}
        "201":
          description: job
          content:
            text/plain:
              schema: {type: string}
        "202":
          description: accepted
`

// テキストの読み方は 200 の応答定義から決めるので、宣言と違う成功ステータス（201）の
// 本文は、同じ読み方では読まずにエラーにする。
func TestExtractor_RejectsUndeclaredSuccessStatus(t *testing.T) {
	file := extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")}
	newExtractor := func(srv *httptest.Server) *Extractor {
		e, err := New(Config{
			SpecPath: writeSpec(t, mixedStatusSpec), OperationID: "ocr", ServerURL: srv.URL,
			MIMETypes: []string{"application/pdf"},
		}, srv.Client())
		require.NoError(t, err)
		return e
	}

	srv, _ := ocrServer(t, http.StatusCreated, "text/plain", "job-1")
	_, err := newExtractor(srv).Extract(t.Context(), file)
	require.ErrorContains(t, err, "returned status 201")

	srv, _ = ocrServer(t, http.StatusOK, "application/json", `{"text":"transcribed"}`)
	text, err := newExtractor(srv).Extract(t.Context(), file)
	require.NoError(t, err)
	require.Equal(t, "transcribed", text)
}

// 201 だけを宣言する operation は、201 の応答を読み、200 は読まない。
func TestExtractor_Declared201Only(t *testing.T) {
	file := extract.SourceFile{MIMEType: "application/pdf", Data: []byte("%PDF")}
	srv, _ := ocrServer(t, http.StatusOK, "application/json", `{"text":"transcribed"}`)
	e, err := New(Config{
		SpecPath: writeSpec(t, jsonSpec), OperationID: "transcribe", ServerURL: srv.URL,
		Params:    map[string]string{"mimeType": "{mime_type}"},
		MIMETypes: []string{"application/pdf"},
	}, srv.Client())
	require.NoError(t, err)
	_, err = e.Extract(t.Context(), file)
	require.ErrorContains(t, err, "returned status 200")
}

// 202 や 204 しか宣言しない operation は、結果を同期で返さないので起動時にエラーにする。
func TestNew_RejectsAsyncOnlyOperation(t *testing.T) {
	const asyncSpec = `
openapi: 3.0.3
info: {title: ocr, version: "1"}
paths:
  /jobs:
    post:
      operationId: createJob
      requestBody:
        content:
          application/octet-stream:
            schema: {type: string, format: binary}
      responses:
        "202":
          description: accepted
          content:
            application/json:
              schema:
                type: object
                properties:
                  id: {type: string}
`
	_, err := New(Config{
		SpecPath: writeSpec(t, asyncSpec), OperationID: "createJob",
		ServerURL: "https://ocr.example.com", MIMETypes: []string{"application/pdf"},
	}, http.DefaultClient)
	require.ErrorContains(t, err, "neither a 200 nor a 201 response")
}
