package oastomcptool

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"
)

// BuildInputSchema の required は schema.Properties（map）を走査して組み立てるため、
// ソートしていないと実行ごとに順序が変わり、生成物（tools.file）との突き合わせが
// 確率的に失敗する。JSON ボディとフォームボディの両方で順序が安定していることを確認する。
func TestBuildInputSchema_RequiredIsDeterministic(t *testing.T) {
	const spec = `{
  "openapi": "3.0.0",
  "info": {"title": "t", "version": "1"},
  "paths": {
    "/json": {"post": {"operationId": "j", "requestBody": {"required": true, "content": {"application/json": {"schema": {
      "type": "object",
      "required": ["zeta", "alpha", "mid"],
      "properties": {"zeta": {"type": "string"}, "alpha": {"type": "string"}, "mid": {"type": "string"}}
    }}}}, "responses": {"200": {"description": "ok"}}}},
    "/form": {"post": {"operationId": "f", "parameters": [{"name": "q", "in": "query", "required": true, "schema": {"type": "string"}}],
      "requestBody": {"content": {"application/x-www-form-urlencoded": {"schema": {
      "type": "object",
      "required": ["zeta", "alpha", "mid"],
      "properties": {"zeta": {"type": "string"}, "alpha": {"type": "string"}, "mid": {"type": "string"}}
    }}}}, "responses": {"200": {"description": "ok"}}}}
  }
}`
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData([]byte(spec))
	require.NoError(t, err)

	for range 20 {
		j := BuildInputSchema(doc.Paths.Find("/json").Post)
		body := j["properties"].(map[string]any)["body"].(map[string]any)
		require.Equal(t, []string{"alpha", "mid", "zeta"}, body["required"])

		f := BuildInputSchema(doc.Paths.Find("/form").Post)
		// パラメータ由来（q）が先頭、フォーム由来はソート済み
		require.Equal(t, []string{"q", "alpha", "mid", "zeta"}, f["required"])
	}
}
