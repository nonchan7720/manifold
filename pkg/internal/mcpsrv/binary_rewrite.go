package mcpsrv

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
)

// isJSONContentType は Content-Type が JSON（application/json または +json）かどうかを返す。
func isJSONContentType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

// decodeBinaryString は base64 文字列（標準/URL セーフ、パディング有無を問わない）をデコードする。
// デコードできない場合は ok=false。
func decodeBinaryString(s string) (raw []byte, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}

// rewriteBinaryFields は JSON ボディ内のバイナリフィールド（base64 文字列）をメディアストレージへ
// アップロードし、値をその URL に置き換える。アップロードごとに resource_link も返す。
// メディアストレージが無効、フィールドが無い、ボディが JSON でない場合は body をそのまま返す。
// null・欠損・base64 としてデコードできない値は変更しない。
func rewriteBinaryFields(
	ctx context.Context,
	body []byte,
	fields []oastomcptool.BinaryField,
	mediaService storage.MediaService,
) ([]byte, []mcp.Content, error) {
	if len(fields) == 0 || !mediaService.Enabled() {
		return body, nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber() // 数値の精度を保つ
	var root any
	if err := dec.Decode(&root); err != nil {
		return body, nil, nil //nolint: nilerr // JSON でなければ何もしない
	}

	var links []mcp.Content
	changed := false
	for _, f := range fields {
		// 配列レスポンスは wrapToolFunc で {"items": [...]} に包まれているため、ここで剥がす
		if m, items, ok := unwrapItems(root, f.Path); ok {
			nv, err := rewriteValue(ctx, items, f.Path, f, mediaService, &links, &changed)
			if err != nil {
				return nil, nil, err
			}
			m["items"] = nv
			continue
		}
		nv, err := rewriteValue(ctx, root, f.Path, f, mediaService, &links, &changed)
		if err != nil {
			return nil, nil, err
		}
		root = nv
	}
	if !changed {
		return body, nil, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(root); err != nil {
		return nil, nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), links, nil
}

// rewriteValue は path をたどって末端の文字列を URL に置き換え、置き換え後の値を返す。
func rewriteValue(
	ctx context.Context,
	v any,
	path []string,
	f oastomcptool.BinaryField,
	media storage.MediaService,
	links *[]mcp.Content,
	changed *bool,
) (any, error) {
	if len(path) == 0 {
		s, ok := v.(string)
		if !ok {
			return v, nil
		}
		raw, ok := decodeBinaryString(s)
		if !ok {
			return v, nil
		}
		// SaveContent は URL セーフ base64（パディング有り）を渡されたときだけデコードするため、
		// 正規化して渡す。生バイトを渡すと、偶然 base64 に見える場合に二重デコードされ得る。
		encoded := []byte(base64.URLEncoding.EncodeToString(raw))
		declared := f.MediaType
		if declared == "" {
			declared = "application/octet-stream"
		}
		contentType := storage.ResolveContentType(declared, encoded)
		link, err := newResourceLink(ctx, encoded, contentType, media)
		if err != nil {
			return nil, err
		}
		*links = append(*links, link)
		*changed = true
		return link.(*mcp.ResourceLink).URI, nil
	}
	seg, rest := path[0], path[1:]
	if seg == oastomcptool.BinaryArrayItem {
		if arr, ok := v.([]any); ok {
			for i := range arr {
				nv, err := rewriteValue(ctx, arr[i], rest, f, media, links, changed)
				if err != nil {
					return nil, err
				}
				arr[i] = nv
			}
		}
		return v, nil
	}
	if m, ok := v.(map[string]any); ok {
		if child, exists := m[seg]; exists && child != nil {
			nv, err := rewriteValue(ctx, child, rest, f, media, links, changed)
			if err != nil {
				return nil, err
			}
			m[seg] = nv
		}
	}
	return v, nil
}

// unwrapItems は path が配列要素から始まり、root が wrapToolFunc による {"items": [...]} の
// 包みである場合に、その map と items を返す。
func unwrapItems(root any, path []string) (map[string]any, any, bool) {
	if len(path) == 0 || path[0] != oastomcptool.BinaryArrayItem {
		return nil, nil, false
	}
	m, ok := root.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, nil, false
	}
	items, ok := m["items"]
	return m, items, ok
}
