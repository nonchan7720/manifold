package client

import (
	"bytes"
	"io"
	"net/http"
)

// unauthorizedResponse は 401 Unauthorized の簡易な JSON レスポンスを組み立てる。
// oauth2RoundTripper と tokenExchangeRoundTrip の両方で使っていた手組みのレスポンス生成を
// 共通化したもの。呼び出し元ごとのエラーメッセージ (msg) はそのまま維持する。
func unauthorizedResponse(msg string) *http.Response {
	body := `{"error":"` + msg + `"}`
	return &http.Response{
		Status:     http.StatusText(http.StatusUnauthorized),
		StatusCode: http.StatusUnauthorized,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(body))),
	}
}
