// Package openapi は、OpenAPI で記述された任意の OCR / 文字起こしサービスを呼ぶ
// Extractor。spec の operation からリクエストの組み立て方（ファイルをどこに入れるか）
// とレスポンスの読み方（テキストがどこにあるか）を決める。
//
// リクエスト本文は、operation の requestBody にある content から次の順で選ぶ。
//
//   - バイナリ（application/octet-stream、application/pdf、image/*、*/* など）:
//     ファイルをそのまま本文にする。
//   - multipart/form-data: `type: string, format: binary` のプロパティにファイルを入れる。
//   - application/json: `type: string, format: byte` のプロパティに base64 で入れる。
//
// レスポンスは text/plain なら本文全体、application/json なら文字列のプロパティを
// テキストとして読む。ファイルやテキストのプロパティが spec から一意に決まらない
// ときは、Config.FileField / Config.TextPath で指定する。
package openapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/nonchan7720/manifold/guardrails/internal/extract"
)

// maxResponseSize はサービスの応答の最大バイト数。
const maxResponseSize = 32 << 20

// mimeTypePlaceholder は Config.Params の値に書ける目印で、呼び出しのたびに
// ファイルの種類（MIME タイプ）に置き換える。
const mimeTypePlaceholder = "{mime_type}"

// Config は呼び出す operation と、spec だけでは決まらない値。
type Config struct {
	// SpecPath は OpenAPI の spec（YAML / JSON）のファイルパス。
	SpecPath string
	// OperationID は呼び出す operation の operationId。
	OperationID string
	// ServerURL はサービスのベース URL。空なら spec の servers の先頭を使う。
	ServerURL string
	// Headers は全リクエストに付けるヘッダー（認証用のトークン等）。
	Headers map[string]string
	// Params は operation のパラメータ（path / query / header）と、本文の
	// ファイル以外のプロパティに入れる値。値の中の {mime_type} はファイルの種類に
	// 置き換える。
	Params map[string]string
	// FileField はファイルを入れる本文のプロパティ名。空なら spec から決める。
	FileField string
	// TextPath は JSON のレスポンスの中でテキストがある場所。"." 区切りのキーで、
	// "*" は配列の全要素（例: "pages.*.text"）。空なら spec から決める。
	TextPath string
	// MIMETypes はこのサービスに送るファイルの種類（"application/pdf"、"image/*" など）。
	MIMETypes []string
}

type bodyKind int

const (
	bodyRaw bodyKind = iota
	bodyMultipart
	bodyJSON
)

// param は operation のパラメータに入れる値。
type param struct {
	name, in, value string
}

// Extractor は OpenAPI の operation を呼んでファイルを書き起こす。
type Extractor struct {
	method  string
	baseURL string
	path    string
	params  []param
	headers map[string]string

	body        bodyKind
	contentType string
	fileField   string
	// fields は本文のファイル以外のプロパティ。JSON では jsonFields を使う。
	fields     map[string]string
	jsonFields map[string]bool // true: 値を JSON としてそのまま入れる（数値・真偽値など）

	jsonResponse bool
	textPath     []string

	mimeTypes  []string
	httpClient *http.Client
}

var _ extract.Extractor = (*Extractor)(nil)

// New は cfg の spec を読み、operation の呼び出し方を決めた Extractor を返す。
// 呼び出し方が spec から決まらない場合は、起動時にここでエラーにする。
func New(cfg Config, httpClient *http.Client) (*Extractor, error) {
	doc, err := openapi3.NewLoader().LoadFromFile(cfg.SpecPath)
	if err != nil {
		return nil, fmt.Errorf("openapi: load spec: %w", err)
	}
	pathName, method, pathItem, op := findOperation(doc, cfg.OperationID)
	if op == nil {
		return nil, fmt.Errorf("openapi: operation %q is not in the spec", cfg.OperationID)
	}
	e := &Extractor{
		method:     method,
		path:       pathName,
		headers:    cfg.Headers,
		mimeTypes:  cfg.MIMETypes,
		httpClient: httpClient,
	}
	if e.baseURL, err = serverURL(doc, cfg.ServerURL); err != nil {
		return nil, err
	}
	if e.params, err = operationParams(pathItem, op, cfg.Params); err != nil {
		return nil, err
	}
	if err := e.setRequestBody(op, cfg); err != nil {
		return nil, err
	}
	if err := e.setResponse(op, cfg.TextPath); err != nil {
		return nil, err
	}
	return e, nil
}

func findOperation(
	doc *openapi3.T, operationID string,
) (pathName, method string, _ *openapi3.PathItem, _ *openapi3.Operation) {
	if doc.Paths == nil {
		return "", "", nil, nil
	}
	for name, item := range doc.Paths.Map() {
		if item == nil {
			continue
		}
		for m, op := range item.Operations() {
			if op != nil && op.OperationID == operationID {
				return name, m, item, op
			}
		}
	}
	return "", "", nil, nil
}

func serverURL(doc *openapi3.T, override string) (string, error) {
	raw := override
	if raw == "" && len(doc.Servers) > 0 && doc.Servers[0] != nil {
		raw = doc.Servers[0].URL
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		strings.ContainsAny(raw, "{}") {
		return "", fmt.Errorf(
			"openapi: the server URL must be an absolute http(s) URL without variables; " +
				"set it explicitly",
		)
	}
	return strings.TrimRight(raw, "/"), nil
}

// operationParams は operation のパラメータに values の値を割り当てる。必須の
// パラメータに値が無ければエラーにする。
func operationParams(
	item *openapi3.PathItem, op *openapi3.Operation, values map[string]string,
) ([]param, error) {
	var out []param
	for _, ref := range slices.Concat(item.Parameters, op.Parameters) {
		if ref == nil || ref.Value == nil {
			continue
		}
		p := ref.Value
		value, ok := values[p.Name]
		if !ok {
			if p.Required || p.In == openapi3.ParameterInPath {
				return nil, fmt.Errorf("openapi: parameter %q (in %s) needs a value", p.Name, p.In)
			}
			continue
		}
		switch p.In {
		case openapi3.ParameterInPath, openapi3.ParameterInQuery, openapi3.ParameterInHeader:
			out = append(out, param{name: p.Name, in: p.In, value: value})
		default:
			return nil, fmt.Errorf("openapi: parameter %q in %s is not supported", p.Name, p.In)
		}
	}
	return out, nil
}

func classifyBody(contentType string) bodyKind {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(contentType)
	}
	switch {
	case mediaType == "multipart/form-data":
		return bodyMultipart
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		return bodyJSON
	default:
		return bodyRaw
	}
}

// setRequestBody は requestBody の content から本文の形を選ぶ。バイナリ、
// multipart、JSON の順に優先し、同じ形が複数あれば名前順で最初のものを使う。
func (e *Extractor) setRequestBody(op *openapi3.Operation, cfg Config) error {
	if op.RequestBody == nil || op.RequestBody.Value == nil ||
		len(op.RequestBody.Value.Content) == 0 {
		return fmt.Errorf(
			"openapi: operation %q has no request body to put the file in",
			cfg.OperationID,
		)
	}
	content := op.RequestBody.Value.Content
	names := make([]string, 0, len(content))
	for name := range content {
		names = append(names, name)
	}
	sort.SliceStable(names, func(i, j int) bool {
		ki, kj := classifyBody(names[i]), classifyBody(names[j])
		if ki != kj {
			return ki < kj
		}
		return names[i] < names[j]
	})
	e.contentType = names[0]
	e.body = classifyBody(e.contentType)
	if e.body == bodyRaw {
		return nil
	}

	var schema *openapi3.Schema
	if mt := content[e.contentType]; mt != nil && mt.Schema != nil {
		schema = mt.Schema.Value
	}
	if schema == nil {
		return fmt.Errorf("openapi: request body %q has no schema", e.contentType)
	}
	format := "binary"
	if e.body == bodyJSON {
		format = "byte"
	}
	e.fileField = cfg.FileField
	if e.fileField == "" {
		var err error
		if e.fileField, err = uniqueStringProperty(schema, format); err != nil {
			return fmt.Errorf("openapi: file field of request body %q: %w; set it explicitly",
				e.contentType, err)
		}
	} else if _, ok := schema.Properties[e.fileField]; !ok {
		return fmt.Errorf("openapi: request body %q has no property %q", e.contentType, e.fileField)
	}

	return e.setBodyFields(schema, cfg.Params)
}

// setBodyFields は本文のファイル以外のプロパティに values の値を割り当てる。必須の
// プロパティに値が無ければエラーにする。
func (e *Extractor) setBodyFields(schema *openapi3.Schema, values map[string]string) error {
	e.fields = map[string]string{}
	e.jsonFields = map[string]bool{}
	for name, ref := range schema.Properties {
		if name == e.fileField {
			continue
		}
		value, ok := values[name]
		if !ok {
			if slices.Contains(schema.Required, name) {
				return fmt.Errorf("openapi: request body property %q needs a value", name)
			}
			continue
		}
		e.fields[name] = value
		// 文字列以外のプロパティは、値が JSON として読めればそのまま入れる。
		if ref != nil && ref.Value != nil && ref.Value.Type != nil && !ref.Value.Type.Is("string") {
			e.jsonFields[name] = json.Valid([]byte(value))
		}
	}
	return nil
}

// uniqueStringProperty は schema のプロパティのうち、`type: string` で format が
// 一致するもの（format が空なら文字列すべて）がちょうど1つならその名前を返す。
func uniqueStringProperty(schema *openapi3.Schema, format string) (string, error) {
	var found []string
	for name, ref := range schema.Properties {
		if ref == nil || ref.Value == nil || ref.Value.Type == nil || !ref.Value.Type.Is("string") {
			continue
		}
		if format == "" || ref.Value.Format == format {
			found = append(found, name)
		}
	}
	sort.Strings(found)
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("no matching string property")
	default:
		return "", fmt.Errorf("several candidates (%s)", strings.Join(found, ", "))
	}
}

// setResponse は成功レスポンスからテキストの読み方を決める。
func (e *Extractor) setResponse(op *openapi3.Operation, textPath string) error {
	var content openapi3.Content
	if resp := successResponse(op); resp != nil {
		content = resp.Content
	}
	var jsonSchema *openapi3.Schema
	hasJSON, hasText := false, false
	for name, mt := range content {
		switch classifyBody(name) {
		case bodyJSON:
			hasJSON = true
			if mt != nil && mt.Schema != nil {
				jsonSchema = mt.Schema.Value
			}
		case bodyRaw:
			if mediaType, _, err := mime.ParseMediaType(name); err == nil &&
				strings.HasPrefix(mediaType, "text/") {
				hasText = true
			}
		case bodyMultipart:
		}
	}
	switch {
	case textPath != "":
		e.jsonResponse = true
		e.textPath = strings.Split(textPath, ".")
	case hasText:
	case hasJSON && jsonSchema != nil:
		name, err := uniqueStringProperty(jsonSchema, "")
		if err != nil {
			return fmt.Errorf(
				"openapi: text of the JSON response: %w; set the text path explicitly",
				err,
			)
		}
		e.jsonResponse = true
		e.textPath = []string{name}
	default:
		return fmt.Errorf(
			"openapi: the response has neither text/plain nor a JSON schema; " +
				"set the text path explicitly",
		)
	}
	return nil
}

func successResponse(op *openapi3.Operation) *openapi3.Response {
	if op.Responses == nil {
		return nil
	}
	codes := make([]string, 0, op.Responses.Len())
	for code := range op.Responses.Map() {
		if strings.HasPrefix(code, "2") {
			codes = append(codes, code)
		}
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		codes = append(codes, "default")
	}
	if ref := op.Responses.Value(codes[0]); ref != nil {
		return ref.Value
	}
	return nil
}

func (e *Extractor) Supports(mimeType string) bool {
	return extract.MatchMIME(e.mimeTypes, mimeType)
}

func (e *Extractor) Extract(ctx context.Context, f extract.SourceFile) (string, error) {
	fill := func(value string) string {
		return strings.ReplaceAll(value, mimeTypePlaceholder, f.MIMEType)
	}
	body, contentType, err := e.buildBody(f, fill)
	if err != nil {
		return "", err
	}

	target := e.path
	query := url.Values{}
	header := http.Header{}
	for _, p := range e.params {
		switch p.in {
		case openapi3.ParameterInPath:
			target = strings.ReplaceAll(target, "{"+p.name+"}", url.PathEscape(fill(p.value)))
		case openapi3.ParameterInQuery:
			query.Add(p.name, fill(p.value))
		case openapi3.ParameterInHeader:
			header.Set(p.name, fill(p.value))
		}
	}
	target = e.baseURL + target
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	httpReq, err := http.NewRequestWithContext(ctx, e.method, target, body)
	if err != nil {
		return "", fmt.Errorf("openapi: build request: %w", err)
	}
	httpReq.Header = header
	httpReq.Header.Set("Content-Type", contentType)
	if e.jsonResponse {
		httpReq.Header.Set("Accept", "application/json")
	} else {
		httpReq.Header.Set("Accept", "text/plain")
	}
	for k, v := range e.headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("openapi: call: %w", err)
	}
	defer resp.Body.Close() //nolint: errcheck
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("openapi: read response: %w", err)
	}
	// 応答の本文はファイルの内容を含みうるので、エラー文には出さない。
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("openapi: returned status %d", resp.StatusCode)
	}
	if len(raw) > maxResponseSize {
		return "", fmt.Errorf("openapi: response exceeds %d bytes", maxResponseSize)
	}
	if !e.jsonResponse {
		return strings.TrimSpace(string(raw)), nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("openapi: decode response: %w", err)
	}
	var texts []string
	if err := collectText(decoded, e.textPath, &texts); err != nil {
		return "", err
	}
	return strings.TrimSpace(strings.Join(texts, "\n")), nil
}

func (e *Extractor) buildBody(
	f extract.SourceFile, fill func(string) string,
) (io.Reader, string, error) {
	switch e.body {
	case bodyMultipart:
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for name, value := range e.fields {
			if err := w.WriteField(name, fill(value)); err != nil {
				return nil, "", fmt.Errorf("openapi: build multipart body: %w", err)
			}
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{
			"name": e.fileField, "filename": "file" + fileExtension(f.MIMEType),
		}))
		h.Set("Content-Type", f.MIMEType)
		part, err := w.CreatePart(h)
		if err == nil {
			_, err = part.Write(f.Data)
		}
		if err == nil {
			err = w.Close()
		}
		if err != nil {
			return nil, "", fmt.Errorf("openapi: build multipart body: %w", err)
		}
		return &buf, w.FormDataContentType(), nil
	case bodyJSON:
		obj := map[string]any{e.fileField: base64.StdEncoding.EncodeToString(f.Data)}
		for name, value := range e.fields {
			if e.jsonFields[name] {
				obj[name] = json.RawMessage(value)
			} else {
				obj[name] = fill(value)
			}
		}
		raw, err := json.Marshal(obj)
		if err != nil {
			return nil, "", fmt.Errorf("openapi: build JSON body: %w", err)
		}
		return bytes.NewReader(raw), "application/json", nil
	case bodyRaw:
	}
	// spec の種類が具体的（application/pdf など）ならそれを、ワイルドカードや
	// application/octet-stream ならファイルの種類を Content-Type にする。
	contentType := f.MIMEType
	if mediaType, _, err := mime.ParseMediaType(e.contentType); err == nil &&
		mediaType != "application/octet-stream" && !strings.Contains(mediaType, "*") {
		contentType = mediaType
	}
	return bytes.NewReader(f.Data), contentType, nil
}

// fileExtension は mimeType の拡張子（".pdf" など）を返す。分からなければ空。
// 拡張子でファイルの種類を決めるサービスのために、multipart のファイル名に付ける。
func fileExtension(mimeType string) string {
	switch mimeType {
	case "application/pdf":
		return ".pdf"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// collectText は v の path が指す文字列を out に集める。キーが無い、または指した先が
// 文字列でない場合はエラーにする（パスの誤りを「テキスト無し」として通さないため）。
// "*" が指す配列が空なのは、テキストが無いという正常な結果として扱う。
func collectText(v any, path []string, out *[]string) error {
	if len(path) == 0 {
		switch leaf := v.(type) {
		case string:
			*out = append(*out, leaf)
			return nil
		case nil:
			return nil
		default:
			return fmt.Errorf("openapi: the text path points at a non-string value")
		}
	}
	key, rest := path[0], path[1:]
	switch node := v.(type) {
	case map[string]any:
		if key == "*" {
			for _, child := range node {
				if err := collectText(child, rest, out); err != nil {
					return err
				}
			}
			return nil
		}
		child, ok := node[key]
		if !ok {
			return fmt.Errorf("openapi: the response has no %q on the text path", key)
		}
		return collectText(child, rest, out)
	case []any:
		if key == "*" {
			for _, child := range node {
				if err := collectText(child, rest, out); err != nil {
					return err
				}
			}
			return nil
		}
		i, err := strconv.Atoi(key)
		if err != nil || i < 0 || i >= len(node) {
			return fmt.Errorf("openapi: the response has no element %q on the text path", key)
		}
		return collectText(node[i], rest, out)
	default:
		return fmt.Errorf("openapi: the response has no %q on the text path", key)
	}
}
