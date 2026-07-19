package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// errorResponse は指定した status の簡易な JSON レスポンスを組み立てる。
// oauth2RoundTripper と tokenExchangeRoundTrip の両方で使っていた手組みのレスポンス生成を
// 共通化したもの。msg は json.Marshal でエスケープしてから埋め込む
// （文字列連結だと msg 中の `"` や制御文字で JSON が壊れるため）。
func errorResponse(status int, msg string) *http.Response {
	body, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		// map[string]string の Marshal は通常失敗しないが、保険として最低限の JSON を返す。
		body = []byte(`{"error":"internal error"}`)
	}
	return &http.Response{
		Status:     http.StatusText(status),
		StatusCode: status,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}

// unauthorizedResponse は 401 Unauthorized の簡易な JSON レスポンスを組み立てる。
func unauthorizedResponse(msg string) *http.Response {
	return errorResponse(http.StatusUnauthorized, msg)
}
