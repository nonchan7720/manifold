package toolsearch

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchemaSearchTexts_Nil_ReturnsNil(t *testing.T) {
	require.Nil(t, schemaSearchTexts(nil))
}

func TestSchemaSearchTexts_NoProperties_ReturnsNil(t *testing.T) {
	require.Nil(t, schemaSearchTexts(map[string]any{"type": "object"}))
}

func TestSchemaSearchTexts_FlatProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"orderId": map[string]any{
				"type":        "string",
				"description": "The order ID to look up",
			},
			"status": map[string]any{
				"type": "string",
				// description なし
			},
		},
	}

	texts := schemaSearchTexts(schema)
	require.ElementsMatch(t, []string{"orderId", "The order ID to look up", "status"}, texts)
}

func TestSchemaSearchTexts_NestedProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"address": map[string]any{
				"type":        "object",
				"description": "Shipping address",
				"properties": map[string]any{
					"city": map[string]any{
						"type":        "string",
						"description": "City name",
					},
				},
			},
		},
	}

	texts := schemaSearchTexts(schema)
	require.ElementsMatch(t, []string{"address", "Shipping address", "city", "City name"}, texts)
}

func TestSchemaSearchTexts_ItemsArray(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filters": map[string]any{
				"type":        "array",
				"description": "List of filters",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"field": map[string]any{
							"type":        "string",
							"description": "Field name to filter on",
						},
					},
				},
			},
		},
	}

	texts := schemaSearchTexts(schema)
	require.ElementsMatch(t, []string{"filters", "List of filters", "field", "Field name to filter on"}, texts)
}

func TestSchemaSearchTexts_ItemsTuple(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"coordinates": map[string]any{
				"type":        "array",
				"description": "Tuple-form coordinate pair",
				"items": []any{
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"lat": map[string]any{"type": "number", "description": "Latitude value"},
						},
					},
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"lon": map[string]any{"type": "number", "description": "Longitude value"},
						},
					},
				},
			},
		},
	}

	texts := schemaSearchTexts(schema)
	require.ElementsMatch(t, []string{"coordinates", "Tuple-form coordinate pair", "lat", "Latitude value", "lon", "Longitude value"}, texts)
}

func TestSchemaSearchTexts_CJKDescription(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"orderId": map[string]any{
				"type":        "string",
				"description": "注文番号",
			},
		},
	}

	texts := schemaSearchTexts(schema)
	require.ElementsMatch(t, []string{"orderId", "注文番号"}, texts)
}

// nonMapSchema は InputSchema が map[string]any 以外の型（上流 MCP の ListTools が
// 返す構造体等を想定）でも json.Marshal/Unmarshal フォールバックで扱えることを確認する。
type nonMapSchemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type nonMapSchema struct {
	Type       string                          `json:"type"`
	Properties map[string]nonMapSchemaProperty `json:"properties"`
}

func TestSchemaSearchTexts_NonMapType_FallsBackToJSONRoundTrip(t *testing.T) {
	schema := nonMapSchema{
		Type: "object",
		Properties: map[string]nonMapSchemaProperty{
			"foo": {Type: "string", Description: "bar description"},
		},
	}

	texts := schemaSearchTexts(schema)
	require.ElementsMatch(t, []string{"foo", "bar description"}, texts)
}

func TestSchemaSearchTexts_JSONRawMessageType_FallsBackToJSONRoundTrip(t *testing.T) {
	raw := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string","description":"y desc"}}}`)

	texts := schemaSearchTexts(raw)
	require.ElementsMatch(t, []string{"x", "y desc"}, texts)
}

func TestSchemaSearchTexts_InvalidType_ReturnsNil(t *testing.T) {
	// json.Marshal できてもトップレベルが object にならない型（配列や関数など）は
	// map へのフォールバックに失敗するため nil を返す。
	texts := schemaSearchTexts(func() {})
	require.Nil(t, texts)
}

// nestedSchema は remaining 段だけネストしたオブジェクトスキーマを構築するテスト用ヘルパー。
// 各階層は "level<N>" という名前のプロパティを持ち、"desc<N>" という description を持つ。
func nestedSchema(remaining int) map[string]any {
	prop := map[string]any{
		"type":        "object",
		"description": fmt.Sprintf("desc%d", remaining),
	}
	if remaining > 1 {
		prop["properties"] = map[string]any{
			fmt.Sprintf("level%d", remaining-1): nestedSchema(remaining - 1),
		}
	}
	return prop
}

func TestSchemaSearchTexts_DepthLimit_StopsDeepRecursion(t *testing.T) {
	const deepLevels = 30
	root := map[string]any{
		"type": "object",
		"properties": map[string]any{
			fmt.Sprintf("level%d", deepLevels-1): nestedSchema(deepLevels - 1),
		},
	}

	texts := schemaSearchTexts(root)

	// 浅い階層は抽出される
	require.Contains(t, texts, fmt.Sprintf("level%d", deepLevels-1))
	// 十分深い階層には到達しない（深さ制限で打ち切られる）
	require.NotContains(t, texts, "level0")
	require.NotContains(t, texts, "level1")
}

func TestSchemaSearchTexts_CyclicSchema_DoesNotHangOrPanic(t *testing.T) {
	m := map[string]any{"type": "object"}
	m["properties"] = map[string]any{"self": m}

	require.NotPanics(t, func() {
		texts := schemaSearchTexts(m)
		require.Contains(t, texts, "self")
	})
}
