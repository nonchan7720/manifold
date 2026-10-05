package toolsearch

import (
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Search は docs を query で検索する。method が空文字の場合は bm25 として扱う。
// method の大文字小文字は区別しない。未知の method はエラーを返す。
//
// docs は呼び出し元が見えるツールだけを渡すこと。検索はその場で行うため、
// ツール認可や絞り込みで隠れたツールが結果に混ざることはない。
// 同じ docs に繰り返し検索するなら、前処理を使い回せる NewIndex を使う。
func Search(docs []ToolDef, query string, method Method, limit int) ([]ToolDef, error) {
	return NewIndex(docs).Search(query, method, limit)
}

// Index は docs に対する検索用の前処理結果（BM25 のトークン頻度表）を保持し、
// 同じ docs への繰り返し検索で前処理を使い回す。前処理は最初の bm25 検索で
// 一度だけ行い、以降は読み取り専用なので複数 goroutine から同時に使える。
type Index struct {
	docs  []ToolDef
	once  sync.Once
	bdocs []bm25Doc
}

// NewIndex は docs の Index を返す。docs は呼び出し元が見えるツールだけを渡すこと
// （Search と同じ）。docs は呼び出し後に変更しないこと。
func NewIndex(docs []ToolDef) *Index {
	return &Index{docs: docs}
}

// Docs は Index が検索対象としているツール定義を返す。
func (ix *Index) Docs() []ToolDef {
	return ix.docs
}

// Search は Index の docs を query で検索する。引数と挙動は Search と同じ。
func (ix *Index) Search(query string, method Method, limit int) ([]ToolDef, error) {
	switch Method(strings.ToLower(string(method))) {
	case "", MethodBM25:
		ix.once.Do(func() { ix.bdocs = buildBM25Docs(ix.docs) })
		return searchBM25Docs(ix.bdocs, query, limit), nil
	case MethodRegexp:
		return searchRegexp(ix.docs, query, limit)
	case MethodFuzzy:
		return searchFuzzy(ix.docs, query, limit), nil
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
