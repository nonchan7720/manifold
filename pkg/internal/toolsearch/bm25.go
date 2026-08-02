package toolsearch

import (
	"math"
	"sort"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// nameWeight は name 側トークンの重み。description 側の 2 倍として扱う。
	nameWeight = 2
)

// bm25Doc は BM25 計算用に前処理したドキュメント。
type bm25Doc struct {
	def    ToolDef
	tf     map[string]int
	length int
}

// searchBM25 は BM25 スコアリングにより docs を query に対してランキングする。
// スコアが 0（クエリトークンが一つも一致しない）のドキュメントは除外し、
// スコア降順、同点は名前昇順で並び替えたうえで limit 件に切り詰める。
// 呼び出しのたびに docs 全体を前処理し直すため、繰り返し呼ぶ場合は
// buildBM25Docs の結果をキャッシュした上で searchBM25Docs を使う方が良い
// （Catalog.Search はサーバーごとに前処理結果をキャッシュしている）。
func searchBM25(docs []ToolDef, query string, limit int) []ToolDef {
	return searchBM25Docs(buildBM25Docs(docs), query, limit)
}

// searchBM25Docs は前処理済みの bdocs（buildBM25Docs の結果）を query に対して
// ランキングする。前処理コストをキャッシュ・再利用したい呼び出し元向けの入口。
func searchBM25Docs(bdocs []bm25Doc, query string, limit int) []ToolDef {
	queryTerms := Tokenize(query)
	if len(queryTerms) == 0 || len(bdocs) == 0 {
		return nil
	}

	idf := computeBM25IDF(bdocs, queryTerms)
	avgdl := computeBM25AvgDL(bdocs)

	scored := scoreBM25Docs(bdocs, queryTerms, idf, avgdl)
	return rankBM25(scored, limit)
}

// buildBM25Docs は各ドキュメントを name（2 倍重み）+ description + 引数名・引数の説明
// （いずれも description と同じ重み 1）のトークン頻度表に変換する。
func buildBM25Docs(docs []ToolDef) []bm25Doc {
	bdocs := make([]bm25Doc, len(docs))
	for i, d := range docs {
		tf := map[string]int{}
		length := 0
		for _, tok := range Tokenize(d.Name) {
			tf[tok] += nameWeight
			length += nameWeight
		}
		for _, tok := range Tokenize(d.Description) {
			tf[tok]++
			length++
		}
		for _, txt := range schemaSearchTexts(d.InputSchema) {
			for _, tok := range Tokenize(txt) {
				tf[tok]++
				length++
			}
		}
		bdocs[i] = bm25Doc{def: d, tf: tf, length: length}
	}
	return bdocs
}

// computeBM25IDF はクエリの各トークンについて、コーパス全体に対する IDF を計算する。
// IDF = ln(1 + (N-df+0.5)/(df+0.5))
func computeBM25IDF(docs []bm25Doc, queryTerms []string) map[string]float64 {
	n := float64(len(docs))
	idf := make(map[string]float64, len(queryTerms))
	for _, term := range queryTerms {
		if _, ok := idf[term]; ok {
			continue
		}
		df := 0
		for _, d := range docs {
			if d.tf[term] > 0 {
				df++
			}
		}
		idf[term] = math.Log(1 + (n-float64(df)+0.5)/(float64(df)+0.5))
	}
	return idf
}

// computeBM25AvgDL はコーパス全体の平均ドキュメント長（重み付きトークン数）を計算する。
func computeBM25AvgDL(docs []bm25Doc) float64 {
	if len(docs) == 0 {
		return 0
	}
	total := 0
	for _, d := range docs {
		total += d.length
	}
	return float64(total) / float64(len(docs))
}

type scoredDoc struct {
	def   ToolDef
	score float64
}

// scoreBM25Docs はクエリトークンごとの BM25 スコアを合算し、スコア 0 のドキュメントを除外する。
func scoreBM25Docs(docs []bm25Doc, queryTerms []string, idf map[string]float64, avgdl float64) []scoredDoc {
	scored := make([]scoredDoc, 0, len(docs))
	for _, d := range docs {
		score := bm25Score(d, queryTerms, idf, avgdl)
		if score > 0 {
			scored = append(scored, scoredDoc{def: d.def, score: score})
		}
	}
	return scored
}

// bm25Score は単一ドキュメントに対するクエリ全体の BM25 スコアを計算する。
func bm25Score(d bm25Doc, queryTerms []string, idf map[string]float64, avgdl float64) float64 {
	var score float64
	for _, term := range queryTerms {
		tf := float64(d.tf[term])
		if tf == 0 {
			continue
		}
		denom := tf + bm25K1*(1-bm25B+bm25B*(float64(d.length)/avgdl))
		score += idf[term] * (tf * (bm25K1 + 1) / denom)
	}
	return score
}

// rankBM25 はスコア降順（同点は名前昇順）で並び替え、limit 件に切り詰める。
func rankBM25(scored []scoredDoc, limit int) []ToolDef {
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].def.Name < scored[j].def.Name
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	result := make([]ToolDef, len(scored))
	for i, s := range scored {
		result[i] = s.def
	}
	return result
}
