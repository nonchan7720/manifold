package oastomcptool

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/nonchan7720/manifold/pkg/internal/resultshape"
)

// ResponseShape は OpenAPI 3 のオペレーションの 2xx の JSON レスポンスから、結果の JSON の形
// （どんなフィールドがあるか）を読む。mediaType は形を読んだレスポンスのメディアタイプ。
// JSON のレスポンスが宣言されていなければ ok は false。
//
// 2xx が複数あるときは最も小さいステータスのものを、メディアタイプが複数あるときは
// application/json を優先して読む。
func ResponseShape(
	operation *openapi3.Operation,
) (shape resultshape.Node, mediaType string, ok bool) {
	if operation == nil || operation.Responses == nil {
		return resultshape.Node{}, "", false
	}
	responses := operation.Responses.Map()
	for _, status := range sortedSuccessStatuses(responses) {
		resp := responses[status]
		if resp == nil || resp.Value == nil {
			continue
		}
		for _, mt := range jsonMediaTypes(slices.Collect(maps.Keys(resp.Value.Content))) {
			media := resp.Value.Content[mt]
			if media == nil || media.Schema == nil || media.Schema.Value == nil {
				continue
			}
			return resultshape.Build(responseSchema{schema: media.Schema.Value}), mt, true
		}
	}
	return resultshape.Node{}, "", false
}

// ResponseShapeSwagger は ResponseShape の Swagger 2 版。メディアタイプは produces
// （オペレーションに無ければ文書全体のもの）から選ぶ。
func ResponseShapeSwagger(
	operation *openapi2.Operation, spec *openapi2.T,
) (shape resultshape.Node, mediaType string, ok bool) {
	if operation == nil || operation.Responses == nil {
		return resultshape.Node{}, "", false
	}
	mediaType, ok = swaggerJSONMediaType(operation, spec)
	if !ok {
		return resultshape.Node{}, "", false
	}
	responses := operation.Responses.Map()
	for _, status := range sortedSuccessStatusKeys(slices.Collect(maps.Keys(responses))) {
		resp := responses[status]
		if resp == nil || resp.Schema == nil {
			continue
		}
		// $ref の解決と循環の扱いを OpenAPI 3 側と揃えるため、変換してから読む（変換後の
		// スキーマは、同じ definitions を同じポインタで共有する）。
		schema := swaggerSchemaToV3(resp.Schema, spec, map[*openapi2.Schema]*openapi3.Schema{})
		if schema == nil {
			continue
		}
		return resultshape.Build(responseSchema{schema: schema}), mediaType, true
	}
	return resultshape.Node{}, "", false
}

// swaggerJSONMediaType は、Swagger 2 のオペレーションが JSON で返すときのメディアタイプを
// 返す。produces が JSON を含まなければ false。produces が無ければ application/json と
// みなす。
func swaggerJSONMediaType(operation *openapi2.Operation, spec *openapi2.T) (string, bool) {
	produces := operation.Produces
	if len(produces) == 0 && spec != nil {
		produces = spec.Produces
	}
	if len(produces) == 0 {
		return "application/json", true
	}
	if types := jsonMediaTypes(produces); len(types) > 0 {
		return types[0], true
	}
	return "", false
}

// jsonMediaTypes は types のうち JSON のものを、application/json を先頭に、残りを名前順で
// 返す（どれを読むかを決定的にするため）。
func jsonMediaTypes(types []string) []string {
	out := slices.DeleteFunc(slices.Clone(types), func(mt string) bool {
		return !isJSONMediaType(mt)
	})
	slices.SortFunc(out, func(a, b string) int {
		return cmp.Or(cmp.Compare(jsonMediaTypeRank(a), jsonMediaTypeRank(b)), cmp.Compare(a, b))
	})
	return out
}

// jsonMediaTypeRank は application/json（パラメータは無視）なら 0、それ以外（+json）なら 1。
func jsonMediaTypeRank(mt string) int {
	base, _, _ := strings.Cut(mt, ";")
	if strings.EqualFold(strings.TrimSpace(base), "application/json") {
		return 0
	}
	return 1
}

// responseSchema は OpenAPI 3 のスキーマを resultshape.Schema として見せるアダプタ。
// kin-openapi の Loader は $ref を解決済み（Value がポインタで共有される）なので、スキーマの
// ポインタをそのまま同一性に使う。
type responseSchema struct {
	schema *openapi3.Schema
}

func (s responseSchema) ID() any { return s.schema }

func (s responseSchema) Alternatives() []resultshape.Schema {
	var out []resultshape.Schema
	for _, refs := range []openapi3.SchemaRefs{s.schema.AllOf, s.schema.AnyOf, s.schema.OneOf} {
		for _, ref := range refs {
			if alt, ok := s.child(ref); ok {
				out = append(out, alt)
			}
		}
	}
	return out
}

func (s responseSchema) Properties() []string {
	return slices.Collect(maps.Keys(s.schema.Properties))
}

func (s responseSchema) Property(name string) (resultshape.Schema, bool) {
	return s.child(s.schema.Properties[name])
}

func (s responseSchema) Items() (resultshape.Schema, bool) { return s.child(s.schema.Items) }

func (s responseSchema) child(ref *openapi3.SchemaRef) (resultshape.Schema, bool) {
	if ref == nil || ref.Value == nil {
		return nil, false
	}
	return responseSchema{schema: ref.Value}, true
}
