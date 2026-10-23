package storage

import (
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

const octetStream = "application/octet-stream"

// ResolveContentType は宣言された Content-Type が実質「不明」のとき、
// 実体の先頭バイトから型を判定して返す。判定できなければ宣言値のまま返す。
//
// 上流 API が application/octet-stream しか返さない場合、resource_link に載る mimeType も
// octet-stream になり、受け手（Claude Code 等）は画像なのか文書なのかを判別できない。
// バイト列を持っているのはこの層だけなので、ここで判定する。
//
// 判定には http.DetectContentType ではなく mimetype を使う。前者は先頭 512 バイトの
// 限られたシグネチャしか見ないため、PDF や Office 文書（docx/xlsx 等の ZIP ベース形式）が
// octet-stream のまま残ることが多い。mimetype はより広い形式に対応し、先頭に改行や BOM が
// 付いた PDF も判定できる。判定できなければ application/octet-stream を返す。
//
// data は SaveContent と同じく base64 で渡ってくることがある。そのままスニッフすると
// text/plain と判定されてしまうため、同じ規則でデコードしてから判定する。
func ResolveContentType(declared string, data []byte) string {
	if !isUndeterminedContentType(declared) || len(data) == 0 {
		return declared
	}
	return mimetype.Detect(decodeBase64(data)).String()
}

// isUndeterminedContentType は Content-Type が型を特定できていない値かどうかを返す。
func isUndeterminedContentType(contentType string) bool {
	base := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	return base == "" || base == octetStream
}
