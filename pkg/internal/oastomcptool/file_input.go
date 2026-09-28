package oastomcptool

import (
	"context"
	"fmt"
	"io"
)

// FileInputSchema returns the MCP input-schema fragment for a file-input
// value, identical to what a `format: binary` OpenAPI property generates
// (see binaryFieldMCPSchema): a bare string (base64 content or a URL) or an
// explicit {url, base64, text, content, filename, contentType} object, marked
// with `_meta.manifold.file: true`. Non-OpenAPI backends use it so a file
// argument is written the same way everywhere.
func FileInputSchema(description string) map[string]any {
	return binaryFieldMCPSchema(description, map[string]any{
		"manifold": map[string]any{
			"file":          true,
			"fileInputHint": fileInputHint,
		},
	})
}

// ResolvedFile is one file argument resolved by ResolveFileInput.
type ResolvedFile struct {
	Data        []byte
	Filename    string
	ContentType string
}

// ResolveFileInput resolves a value shaped by FileInputSchema into bytes
// under the package-level FileFetchConfig (URL allow list, size cap), the
// same way OpenAPI multipart uploads do (writeMultipartFile). name only
// labels errors. An unset contentType is sniffed from the content.
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
	// resolveFileFieldValue bounds base64/text by MaxSize itself and
	// fetchFileFromURL wraps the download in a MaxSize reader, so LimitReader
	// here is only a guard against a stream that ignores those.
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
