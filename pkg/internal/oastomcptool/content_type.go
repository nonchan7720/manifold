package oastomcptool

import (
	"slices"
	"strings"
)

// textualApplicationTypes はテキストとして扱う application/* の Content-Type。
var textualApplicationTypes = []string{
	"application/json",
	"application/xml",
	"application/yaml",
	// 非標準パターン
	"application/x-yaml",
	"application/yml",
}

// binaryMediaPrefixes は、接尾辞規則（+xml / +json）より優先して非テキストとして扱うメディア種別のプレフィックス。
// image/svg+xml のように XML 系の接尾辞を持つが画像として扱うべき型が、テキスト扱いされないようにする。
var binaryMediaPrefixes = []string{
	"image/",
	"audio/",
	"video/",
}

// IsTextContentType は Content-Type がテキスト系（text/*、JSON、XML、YAML 等）かを返す。
// パラメータ（; charset=utf-8 等）は無視し、大文字小文字も区別しない。
// 空文字は判定不能のためテキストとは見なさない。
// image/ audio/ video/ は、接尾辞が +xml / +json であってもテキストとは見なさない
// （例: image/svg+xml は画像として扱う）。
func IsTextContentType(contentType string) bool {
	baseType, _, _ := strings.Cut(contentType, ";")
	baseType = strings.ToLower(strings.TrimSpace(baseType))
	if baseType == "" {
		return false
	}
	for _, prefix := range binaryMediaPrefixes {
		if strings.HasPrefix(baseType, prefix) {
			return false
		}
	}
	return strings.HasPrefix(baseType, "text/") ||
		strings.HasSuffix(baseType, "+json") ||
		strings.HasSuffix(baseType, "+xml") ||
		slices.Contains(textualApplicationTypes, baseType)
}

// shouldBase64EncodeResponse はバイナリ応答を base64 化すべきかを返す。
// 成功（2xx）かつ実際の Content-Type がテキスト系でない場合のみ true。
// ResponseIsBinary が 2xx の応答だけを binary と判定するのに合わせ、3xx（304 等）や
// エラー応答、JSON/テキストの応答（202 のステータス等）は生のまま返す。
func shouldBase64EncodeResponse(isBinaryResponse bool, statusCode int, contentType string) bool {
	return isBinaryResponse && statusCode < 300 && !IsTextContentType(contentType)
}
