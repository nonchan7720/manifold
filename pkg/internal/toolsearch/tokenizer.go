package toolsearch

import (
	"strings"
	"unicode"
)

// Tokenize は文字列を検索用トークン列に分割する。
// ASCII を含む一般的な単語は非英数字を区切り文字として単語単位でトークン化し、
// CJK（漢字/ひらがな/カタカナ/ハングル）はスペースなしで連続することが多いため、
// 2 文字単位のバイグラムに分割する（1 文字だけの連続はそのまま単独トークンとする）。
// 例: "注文検索" -> ["注文", "文検", "検索"]
func Tokenize(s string) []string {
	s = strings.ToLower(s)

	var tokens []string
	runes := []rune(s)
	n := len(runes)

	for i := 0; i < n; {
		r := runes[i]
		switch {
		case isCJK(r):
			j := i
			for j < n && isCJK(runes[j]) {
				j++
			}
			tokens = append(tokens, bigrams(runes[i:j])...)
			i = j
		case isWordRune(r):
			j := i
			for j < n && isWordRune(runes[j]) {
				j++
			}
			tokens = append(tokens, string(runes[i:j]))
			i = j
		default:
			i++
		}
	}
	return tokens
}

// isCJK は漢字・ひらがな・カタカナ・ハングルのいずれかに属する文字かどうかを返す。
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// isWordRune は ASCII 単語トークンの構成要素とみなす文字（英数字とアンダースコア）かどうかを返す。
// CJK はバイグラム側で扱うためここでは除外する。
func isWordRune(r rune) bool {
	if isCJK(r) {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// bigrams は連続する CJK ルーン列を 2 文字ずつのバイグラムに分割する。
// 長さが 1 の場合はそのまま単独トークンとして返す。
func bigrams(runes []rune) []string {
	if len(runes) == 0 {
		return nil
	}
	if len(runes) == 1 {
		return []string{string(runes)}
	}
	tokens := make([]string, 0, len(runes)-1)
	for i := 0; i < len(runes)-1; i++ {
		tokens = append(tokens, string(runes[i:i+2]))
	}
	return tokens
}
