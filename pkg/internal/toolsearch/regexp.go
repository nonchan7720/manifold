package toolsearch

import "regexp"

// searchRegexp は query を大文字小文字を区別しない正規表現としてコンパイルし、
// name / description / 引数名 / 引数の説明のいずれかにマッチしたドキュメントを
// 元の順序のまま limit 件返す。不正な正規表現の場合はコンパイルエラーをそのまま返す。
// query が Tokenize で1つもトークンを生成しない場合（空文字や "." のような記号のみの
// クエリ）は、bm25/fuzzy と挙動を揃えて早期に nil（該当なし）を返す。そうしないと、
// 例えば "." は空パターンと同様に全文字列にマッチしてしまい、カタログ全体が
// 返ってしまう。
func searchRegexp(docs []ToolDef, query string, limit int) ([]ToolDef, error) {
	if len(Tokenize(query)) == 0 {
		return nil, nil
	}

	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return nil, err
	}

	var matched []ToolDef
	for _, d := range docs {
		if re.MatchString(d.Name) || re.MatchString(d.Description) || matchesAny(re, schemaSearchTexts(d.InputSchema)) {
			matched = append(matched, d)
			if limit > 0 && len(matched) >= limit {
				break
			}
		}
	}
	return matched, nil
}

// matchesAny は texts のいずれかが re にマッチすれば true を返す。
func matchesAny(re *regexp.Regexp, texts []string) bool {
	for _, t := range texts {
		if re.MatchString(t) {
			return true
		}
	}
	return false
}
