package toolsearch

import "fmt"

// ResultFormat は tool_search の検索結果の出力フォーマットを表す。
type ResultFormat string

const (
	// ResultFormatDefault は従来どおり []ToolDef（name/description/inputSchema）を返す。
	ResultFormatDefault ResultFormat = "default"
	// ResultFormatClaude は Claude API の Tool Search Tool のカスタム検索実装規約
	// (https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool#custom-tool-search-implementation)
	// に準拠した tool_reference ブロック（[]ToolReference）を返す。
	ResultFormatClaude ResultFormat = "claude"
)

// ToolReference は Claude API が tool_result content 内で完全なツール定義に自動展開する
// tool_reference ブロックの形。
type ToolReference struct {
	Type     string `json:"type"`
	ToolName string `json:"tool_name"`
}

// FormatResults は検索結果 defs を format に応じた出力形式へ変換する。
// format が空文字または ResultFormatDefault の場合は []ToolDef を返し、
// ResultFormatClaude の場合は []ToolReference を返す。いずれの場合もヒット 0 件時は
// JSON マーシャル時に null ではなく空配列 [] になるよう非 nil のスライスを返す。
// 未知の format はエラーを返す。
func FormatResults(format ResultFormat, defs []ToolDef) (any, error) {
	switch format {
	case "", ResultFormatDefault:
		out := defs
		if out == nil {
			out = []ToolDef{}
		}
		return out, nil
	case ResultFormatClaude:
		refs := make([]ToolReference, len(defs))
		for i, d := range defs {
			refs[i] = ToolReference{Type: "tool_reference", ToolName: d.Name}
		}
		return refs, nil
	default:
		return nil, fmt.Errorf("unknown tool_search result format: %s", format)
	}
}
