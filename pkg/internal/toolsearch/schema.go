package toolsearch

import "encoding/json"

// schemaMaxDepth は schemaSearchTexts の再帰の深さ上限。異常に深いネストや、
// （通常の JSON デコード結果では起こらないはずだが）循環参照を含む入力に対して
// 無限再帰・スタックオーバーフローを防ぐためのガード。
const schemaMaxDepth = 8

// schemaSearchTexts は JSON Schema（典型的には ToolDef.InputSchema）から、検索対象と
// なる引数名（properties の各キー）と引数の説明（各 property の description）を
// 再帰的に抽出する。ネストしたオブジェクト（properties 内の properties）や配列
// （items）も辿る。InputSchema が map[string]any でない場合（上流 MCP の ListTools が
// 返す構造体等）は json.Marshal/Unmarshal で map 化するフォールバックを行う。
// 抽出対象が無い、あるいは schema が解釈できない場合は nil を返す。
func schemaSearchTexts(schema any) []string {
	m, ok := asSchemaMap(schema)
	if !ok {
		return nil
	}
	var texts []string
	walkSchemaForSearchTexts(m, 0, &texts)
	return texts
}

// asSchemaMap は schema を map[string]any として解釈する。既に map[string]any であれば
// そのまま返し、それ以外は JSON ラウンドトリップ（Marshal → Unmarshal）でのフォールバックを
// 試みる。どちらも失敗する場合は ok=false を返す。
func asSchemaMap(schema any) (map[string]any, bool) {
	if schema == nil {
		return nil, false
	}
	if m, ok := schema.(map[string]any); ok {
		return m, true
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

// walkSchemaForSearchTexts は schema の properties/items を再帰的に辿り、
// texts に引数名・引数の説明を追加していく。
func walkSchemaForSearchTexts(schema map[string]any, depth int, texts *[]string) {
	if depth > schemaMaxDepth {
		return
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		for name, propRaw := range props {
			*texts = append(*texts, name)

			propMap, ok := asSchemaMap(propRaw)
			if !ok {
				continue
			}
			if desc, ok := propMap["description"].(string); ok && desc != "" {
				*texts = append(*texts, desc)
			}
			walkSchemaForSearchTexts(propMap, depth+1, texts)
		}
	}

	if itemsRaw, ok := schema["items"]; ok {
		if itemsMap, ok := asSchemaMap(itemsRaw); ok {
			walkSchemaForSearchTexts(itemsMap, depth+1, texts)
		}
	}
}
