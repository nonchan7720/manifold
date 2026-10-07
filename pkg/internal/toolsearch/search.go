package toolsearch

import (
	"fmt"
	"slices"
	"strings"
)

// Search は docs を query で検索する。method が空文字の場合は bm25 として扱う。
// method の大文字小文字は区別しない。未知の method はエラーを返す。
//
// docs は呼び出し元が見えるツールだけを渡すこと。検索はその場で行うため、
// ツール認可や絞り込みで隠れたツールが結果に混ざることはない。
func Search(docs []ToolDef, query string, method Method, limit int) ([]ToolDef, error) {
	switch Method(strings.ToLower(string(method))) {
	case "", MethodBM25:
		return searchBM25(docs, query, limit), nil
	case MethodRegexp:
		return searchRegexp(docs, query, limit)
	case MethodFuzzy:
		return searchFuzzy(docs, query, limit), nil
	default:
		return nil, fmt.Errorf("unknown tool_search method: %s", method)
	}
}

// DigestEntry は Digest が返す 1 ツール分の要約（ツール名 + 説明）。
type DigestEntry struct {
	Name        string
	Description string
}

// Digest は docs をツール名のアルファベット順に並べた DigestEntry のスライスとして返す。
// tool_search の description に含めるダイジェスト文の材料として使う（毎回決定的な
// 出力になるようソートする）。説明文の切り詰めなどの表示上の加工は呼び出し元の責務。
// docs が空なら nil を返す。
func Digest(docs []ToolDef) []DigestEntry {
	if len(docs) == 0 {
		return nil
	}
	entries := make([]DigestEntry, len(docs))
	for i, d := range docs {
		entries[i] = DigestEntry{Name: d.Name, Description: d.Description}
	}
	slices.SortFunc(entries, func(a, b DigestEntry) int {
		return strings.Compare(a.Name, b.Name)
	})
	return entries
}
