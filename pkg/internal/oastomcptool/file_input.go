package oastomcptool

import (
	"context"
	"fmt"
	"io"
)

// FileInputSchema はファイル入力値の MCP input schema 断片を返す。OpenAPI の
// `format: binary` プロパティが生成するもの（binaryFieldMCPSchema 参照）と同一で、
// 文字列（base64 の内容または URL）か、明示的な {url, base64, text, content,
// filename, contentType} オブジェクトを受け付け、`_meta.manifold.file: true` を付ける。
// OpenAPI 以外のバックエンドでも、ファイル引数の書き方を揃えるために使う。
func FileInputSchema(description string) map[string]any {
	return binaryFieldMCPSchema(description, map[string]any{
		"manifold": map[string]any{
			"file":          true,
			"fileInputHint": fileInputHint,
		},
	})
}

// ResolvedFile は ResolveFileInput で解決したファイル引数 1 つ。
type ResolvedFile struct {
	Data        []byte
	Filename    string
	ContentType string
}

// ResolveFileInput は FileInputSchema 形式の値を、パッケージレベルの
// FileFetchConfig（URL 許可リスト、サイズ上限）に従ってバイト列へ解決する。
// OpenAPI の multipart アップロード（writeMultipartFile）と同じ処理。name は
// エラーメッセージのラベルにのみ使う。contentType が無ければ内容から推定する。
func ResolveFileInput(ctx context.Context, name string, value any) (ResolvedFile, error) {
	filename := defaultFileFieldName
	filenameExplicit := false
	contentType := ""
	contentTypeExplicit := false
	if m, ok := value.(map[string]any); ok {
		if fn, ok := m["filename"].(string); ok && fn != "" {
			filename = fn
			filenameExplicit = true
		}
		if ct, ok := m["contentType"].(string); ok && ct != "" {
			contentType = ct
			contentTypeExplicit = true
		}
	}

	body, filename, contentType, err := resolveFileFieldValue(
		ctx, name, value, filename, filenameExplicit, contentType, contentTypeExplicit,
	)
	if err != nil {
		return ResolvedFile{}, err
	}
	if body == nil {
		return ResolvedFile{}, fmt.Errorf("%q: empty file content", name)
	}
	defer body.Close() //nolint: errcheck

	cfg := getFileFetchConfig()
	// base64/text は resolveFileFieldValue 自身が MaxSize で制限し、URL は
	// fetchFileFromURL が MaxSize の reader で包むため、ここでの LimitReader は
	// それらを無視するストリームに対する保険にすぎない。
	data, err := io.ReadAll(io.LimitReader(body, cfg.MaxSize+1))
	if err != nil {
		return ResolvedFile{}, fmt.Errorf("%q: read file content: %w", name, err)
	}
	if int64(len(data)) > cfg.MaxSize {
		return ResolvedFile{}, fmt.Errorf(
			"%q: file size exceeds the maximum allowed size of %d bytes", name, cfg.MaxSize,
		)
	}
	return ResolvedFile{Data: data, Filename: filename, ContentType: contentType}, nil
}
