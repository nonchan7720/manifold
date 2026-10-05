package oastomcptool

import (
	"slices"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
)

// BinaryArrayItem は BinaryField.Path 内で「配列の各要素」を表すセグメント。
// プロパティ名と衝突しないよう制御文字を含む値にしている。
const BinaryArrayItem = "\x00items"

// BinaryField は 2xx レスポンスの JSON ボディ内で format: binary（Swagger 2 は type: file も）
// と宣言されているフィールドの位置を表す。
type BinaryField struct {
	// Path はルートからのプロパティ名の列。配列要素は BinaryArrayItem で表す。
	Path []string
	// MediaType はスキーマの contentMediaType（OpenAPI 3.1）。無ければ空。
	MediaType string
}

// ResponseBinaryFields は OpenAPI 3 オペレーションの 2xx JSON レスポンスから、
// バイナリフィールドの位置を検出する。ボディ全体が binary の場合は
// ResponseIsBinary の担当なので、ここでは含めない。
func ResponseBinaryFields(operation *openapi3.Operation) []BinaryField {
	if operation == nil || operation.Responses == nil {
		return nil
	}
	var fields []BinaryField
	for _, status := range sortedSuccessStatuses(operation.Responses.Map()) {
		resp := operation.Responses.Map()[status]
		if resp == nil || resp.Value == nil {
			continue
		}
		for mt, media := range resp.Value.Content {
			if !isJSONMediaType(mt) || media == nil || media.Schema == nil {
				continue
			}
			walkBinary3(media.Schema.Value, nil, map[*openapi3.Schema]bool{}, &fields)
		}
	}
	return dedupeBinaryFields(fields)
}

// ResponseBinaryFieldsSwagger は Swagger 2 オペレーションの 2xx レスポンスから、
// バイナリフィールドの位置を検出する。$ref は spec.Definitions から解決する。
func ResponseBinaryFieldsSwagger(operation *openapi2.Operation, spec *openapi2.T) []BinaryField {
	if operation == nil {
		return nil
	}
	// produces が指定されていて JSON を含まない場合は JSON ボディではない
	produces := operation.Produces
	if len(produces) == 0 && spec != nil {
		produces = spec.Produces
	}
	if len(produces) > 0 && !slices.ContainsFunc(produces, isJSONMediaType) {
		return nil
	}
	var fields []BinaryField
	statuses := make([]string, 0, len(operation.Responses))
	for s := range operation.Responses {
		statuses = append(statuses, s)
	}
	for _, status := range sortedSuccessStatusKeys(statuses) {
		resp := operation.Responses[status]
		if resp == nil || resp.Schema == nil {
			continue
		}
		walkBinary2(resp.Schema, spec, nil, map[*openapi2.Schema]bool{}, &fields)
	}
	return dedupeBinaryFields(fields)
}

func isJSONMediaType(mt string) bool {
	mt = strings.ToLower(strings.TrimSpace(strings.SplitN(mt, ";", 2)[0]))
	return mt == "application/json" || strings.HasSuffix(mt, "+json")
}

func sortedSuccessStatuses(m map[string]*openapi3.ResponseRef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return sortedSuccessStatusKeys(keys)
}

// sortedSuccessStatusKeys は 2xx のステータスだけを昇順で返す（出力を決定的にするため）。
func sortedSuccessStatusKeys(keys []string) []string {
	var out []string
	for _, k := range keys {
		if n, err := strconv.Atoi(k); err == nil && n >= 200 && n < 300 {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func dedupeBinaryFields(fields []BinaryField) []BinaryField {
	var out []BinaryField
	for _, f := range fields {
		if len(f.Path) == 0 {
			continue
		}
		if !slices.ContainsFunc(
			out,
			func(o BinaryField) bool { return slices.Equal(o.Path, f.Path) },
		) {
			out = append(out, f)
		}
	}
	return out
}

func appendSeg(path []string, seg string) []string {
	return append(slices.Clone(path), seg)
}

// walkBinary3 は schema を再帰的にたどり、binary フィールドを fields に追加する。
// allOf/oneOf/anyOf は同じ位置のスキーマとして扱う。visited は循環参照対策（現在の探索経路のみ）。
func walkBinary3(
	schema *openapi3.Schema,
	path []string,
	visited map[*openapi3.Schema]bool,
	fields *[]BinaryField,
) {
	if schema == nil || visited[schema] {
		return
	}
	visited[schema] = true
	defer delete(visited, schema)

	if schema.Format == "binary" {
		*fields = append(*fields, BinaryField{Path: path, MediaType: schema.ContentMediaType})
		return
	}
	for _, refs := range []openapi3.SchemaRefs{schema.AllOf, schema.OneOf, schema.AnyOf} {
		for _, r := range refs {
			if r != nil {
				walkBinary3(r.Value, path, visited, fields)
			}
		}
	}
	if schema.Items != nil {
		walkBinary3(schema.Items.Value, appendSeg(path, BinaryArrayItem), visited, fields)
	}
	for name, p := range schema.Properties {
		if p != nil {
			walkBinary3(p.Value, appendSeg(path, name), visited, fields)
		}
	}
}

// walkBinary2 は walkBinary3 の Swagger 2 版。$ref は Definitions から解決する。
func walkBinary2(
	ref *openapi2.SchemaRef,
	spec *openapi2.T,
	path []string,
	visited map[*openapi2.Schema]bool,
	fields *[]BinaryField,
) {
	var schema *openapi2.Schema
	if spec != nil {
		schema = resolveSwaggerSchemaRef(ref, spec)
	} else if ref != nil {
		schema = ref.Value
	}
	if schema == nil || visited[schema] {
		return
	}
	visited[schema] = true
	defer delete(visited, schema)

	if schema.Format == "binary" || (schema.Type != nil && schema.Type.Is("file")) {
		*fields = append(*fields, BinaryField{Path: path})
		return
	}
	for _, r := range schema.AllOf {
		walkBinary2(r, spec, path, visited, fields)
	}
	if schema.Items != nil {
		walkBinary2(schema.Items, spec, appendSeg(path, BinaryArrayItem), visited, fields)
	}
	for name, p := range schema.Properties {
		walkBinary2(p, spec, appendSeg(path, name), visited, fields)
	}
}
