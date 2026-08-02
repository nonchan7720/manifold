package toolsearch

// ToolDef は tool_search が返すツール定義。実ツールを tools/call するために
// 必要な name / description / inputSchema をそのまま JSON で保持する。
type ToolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

// Method は tool_search が対応する検索アルゴリズムを表す。
type Method string

const (
	// MethodBM25 は BM25 スコアリングによる全文検索（デフォルト）。
	MethodBM25 Method = "bm25"
	// MethodRegexp は正規表現による name/description マッチ検索。
	MethodRegexp Method = "regexp"
	// MethodFuzzy は曖昧一致（fuzzy）検索。
	MethodFuzzy Method = "fuzzy"
)
