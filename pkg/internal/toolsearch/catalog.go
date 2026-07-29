package toolsearch

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Catalog は全 mcpServers 分のツール定義を集約する。集計（Total）は全サーバー横断、
// 検索（Search）はサーバー単位のスコープで行う。
type Catalog struct {
	mu       sync.RWMutex
	byServer map[string][]ToolDef

	// bm25Cache はサーバーごとの BM25 前処理済みドキュメント（server string -> []bm25Doc）。
	// sync.Map.LoadOrStore により、同一サーバーへの同時初回アクセス同士が同じ前処理結果に
	// 収束することを保証する（pkg/internal/client.InMemoryRegistry.GetOrCreate と同じパターン）。
	bm25Cache sync.Map
}

// NewCatalog は空の Catalog を生成する。
func NewCatalog() *Catalog {
	return &Catalog{byServer: map[string][]ToolDef{}}
}

// Add は指定サーバーにツール定義を追加する。同名のツールが既に存在する場合は
// 二重計上せず内容を置き換える。BM25 用の前処理キャッシュ（bm25Cache）は
// 内容が古くなるため、対象サーバーの分を破棄する。
func (c *Catalog) Add(server string, defs ...ToolDef) {
	c.mu.Lock()
	defer c.mu.Unlock()

	existing := c.byServer[server]
	index := make(map[string]int, len(existing))
	for i, d := range existing {
		index[d.Name] = i
	}
	for _, d := range defs {
		if i, ok := index[d.Name]; ok {
			existing[i] = d
			continue
		}
		index[d.Name] = len(existing)
		existing = append(existing, d)
	}
	c.byServer[server] = existing
	c.bm25Cache.Delete(server)
}

// Total は全サーバー合計のツール数を返す。
func (c *Catalog) Total() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	total := 0
	for _, defs := range c.byServer {
		total += len(defs)
	}
	return total
}

// DigestEntry は Digest が返す 1 ツール分の要約（ツール名 + 説明）。
type DigestEntry struct {
	Name        string
	Description string
}

// Digest は指定サーバーの登録ツール全件を、ツール名のアルファベット順ソートで
// DigestEntry（Name + Description）のスライスとして返す。tool_search の description に
// 含めるダイジェスト文の材料として使う（毎回決定的な出力になるようソートする）。
// 説明文の切り詰めなどの表示上の加工は呼び出し元（mcpsrv）の責務とする。
// 未知のサーバーは nil を返す。
func (c *Catalog) Digest(server string) []DigestEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	defs := c.byServer[server]
	if len(defs) == 0 {
		return nil
	}

	entries := make([]DigestEntry, len(defs))
	for i, d := range defs {
		entries[i] = DigestEntry{Name: d.Name, Description: d.Description}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries
}

// Search は指定サーバーのスコープ内でツール定義を検索する。method が空文字の場合は
// bm25 として扱う。method の大文字小文字は区別しない。未知の method はエラーを返す。
func (c *Catalog) Search(server, query string, method Method, limit int) ([]ToolDef, error) {
	switch Method(strings.ToLower(string(method))) {
	case "", MethodBM25:
		return searchBM25Docs(c.bm25Docs(server), query, limit), nil
	case MethodRegexp:
		return searchRegexp(c.docsFor(server), query, limit)
	case MethodFuzzy:
		return searchFuzzy(c.docsFor(server), query, limit), nil
	default:
		return nil, fmt.Errorf("unknown tool_search method: %s", method)
	}
}

// docsFor は指定サーバーのツール定義のスナップショットを返す。
func (c *Catalog) docsFor(server string) []ToolDef {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]ToolDef(nil), c.byServer[server]...)
}

// bm25Docs は指定サーバーの BM25 前処理済みドキュメント集合を返す。Add で当該
// サーバーのツールが変更されるまでキャッシュを再利用し、tool_search 呼び出しの
// たびにトークナイズをやり直すコストを避ける。
func (c *Catalog) bm25Docs(server string) []bm25Doc {
	if v, ok := c.bm25Cache.Load(server); ok {
		return v.([]bm25Doc)
	}
	bdocs := buildBM25Docs(c.docsFor(server))
	actual, _ := c.bm25Cache.LoadOrStore(server, bdocs)
	return actual.([]bm25Doc)
}
