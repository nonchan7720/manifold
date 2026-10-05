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

// IsTextContentType は Content-Type がテキスト系（text/*、JSON、XML、YAML 等）かを返す。
// パラメータ（; charset=utf-8 等）は無視し、大文字小文字も区別しない。
// 空文字は判定不能のためテキストとは見なさない。
func IsTextContentType(contentType string) bool {
	baseType, _, _ := strings.Cut(contentType, ";")
	baseType = strings.ToLower(strings.TrimSpace(baseType))
	if baseType == "" {
		return false
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
