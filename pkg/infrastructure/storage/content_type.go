package storage

import (
	"net/http"
	"strings"
)

const octetStream = "application/octet-stream"

// ResolveContentType は宣言された Content-Type が実質「不明」のとき、
// 実体の先頭バイトから型を判定して返す。判定できなければ宣言値のまま返す。
//
// 上流 API が application/octet-stream しか返さない場合、resource_link に載る mimeType も
// octet-stream になり、受け手（Claude Code 等）は画像なのか文書なのかを判別できない。
// バイト列を持っているのはこの層だけなので、ここで判定する。
//
// data は SaveContent と同じく base64 で渡ってくることがある。そのままスニッフすると
// text/plain と判定されてしまうため、同じ規則でデコードしてから判定する。
func ResolveContentType(declared string, data []byte) string {
	if !isUndeterminedContentType(declared) || len(data) == 0 {
		return declared
	}
	return http.DetectContentType(decodeBase64(data))
}

// isUndeterminedContentType は Content-Type が型を特定できていない値かどうかを返す。
func isUndeterminedContentType(contentType string) bool {
	base := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	return base == "" || base == octetStream
}
