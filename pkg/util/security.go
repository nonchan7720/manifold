package util

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
)

// SanitizeLog removes or escapes control characters like CRLF to prevent log injection.
func SanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// redactedValue はログ上でマスクした値の置き換え文字列。
const redactedValue = "REDACTED"

// sensitiveQueryParams はログに残してはいけないクエリパラメータ名（小文字）。
// OAuth の認可コード・state・PKCE・各種トークン・クライアント資格情報を含む。
var sensitiveQueryParams = map[string]struct{}{
	"code":             {},
	"state":            {},
	"code_challenge":   {},
	"code_verifier":    {},
	"client_secret":    {},
	"client_assertion": {},
	"access_token":     {},
	"refresh_token":    {},
	"id_token":         {},
	"id_token_hint":    {},
	"token":            {},
	"device_code":      {},
	"user_code":        {},
	"password":         {},
	"api_key":          {},
}

// RedactRequestURI は RequestURI のクエリ文字列のうち機密パラメータの値を
// マスクした文字列を返す。パスやその他のパラメータはそのまま残す。
// クエリが解析できない場合はクエリ全体をマスクする。
func RedactRequestURI(requestURI string) string {
	path, rawQuery, found := strings.Cut(requestURI, "?")
	if !found || rawQuery == "" {
		return SanitizeLog(requestURI)
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return SanitizeLog(path) + "?" + redactedValue
	}
	for k := range values {
		if _, ok := sensitiveQueryParams[strings.ToLower(k)]; ok {
			values[k] = []string{redactedValue}
		}
	}
	return SanitizeLog(path + "?" + values.Encode())
}

// Fingerprint は機密値そのものを記録せずにログ上で相関を取るための識別子を返す。
// SHA-256 の先頭 12 桁（hex）。空文字列には空文字列を返す。
func Fingerprint(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
