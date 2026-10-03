package toolsearch

import (
	"strings"

	"github.com/sahilm/fuzzy"
)

// toolDefSource は []ToolDef を fuzzy.Source として扱うための薄いラッパー。
// マッチ対象の文字列は事前に "Name Description 引数名... 引数の説明..." を連結した
// ものを保持しておく（fuzzy ライブラリが String(i) を複数回呼び得るため、スキーマの
// 再帰抽出を都度行わずに 1 回だけ計算しておく）。
type toolDefSource struct {
	texts []string
}

// newToolDefSource は docs から検索対象テキスト（Name/Description/引数名/引数の説明）を
// 1 回だけ計算して toolDefSource を構築する。
func newToolDefSource(docs []ToolDef) toolDefSource {
	texts := make([]string, len(docs))
	for i, d := range docs {
		parts := append([]string{d.Name, d.Description}, schemaSearchTexts(d.InputSchema)...)
		texts[i] = strings.Join(parts, " ")
	}
	return toolDefSource{texts: texts}
}

func (s toolDefSource) String(i int) string {
	return s.texts[i]
}

func (s toolDefSource) Len() int {
	return len(s.texts)
}

// searchFuzzy は sahilm/fuzzy による曖昧一致検索（name / description / 引数名 /
// 引数の説明を対象）を行い、スコア降順（fuzzy.FindFrom が既に降順ソート済み）で
// limit 件に切り詰めて返す。
func searchFuzzy(docs []ToolDef, query string, limit int) []ToolDef {
	if len(docs) == 0 {
		return nil
	}

	matches := fuzzy.FindFrom(query, newToolDefSource(docs))
	if len(matches) == 0 {
		return nil
	}
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}

	result := make([]ToolDef, len(matches))
	for i, m := range matches {
		result[i] = docs[m.Index]
	}
	return result
}
