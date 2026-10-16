package middleware

import "net/http"

// MaxBodyBytes はリクエスト本文を最大 limit バイトに制限するミドルウェアを返す。
// 超過分を読むとボディの読み取りがエラーになり、サーバーは 413 を返す。
// limit <= 0 の場合は何もしない（無制限）。
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limit <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 宣言された Content-Length が上限超過なら本文を読む前に弾く。
			if r.ContentLength > limit {
				http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}
