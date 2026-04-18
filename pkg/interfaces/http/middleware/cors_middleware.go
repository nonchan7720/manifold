package middleware

import (
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
)

func CorsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = trace.StartSpan(ctx, "Middleware/CORS")
		defer func() { trace.EndSpan(ctx, nil) }()
		*r = *r.WithContext(ctx)

		// 1. 許可するオリジン（InspectorのURLに合わせて変更してください）
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// 2. 許可するメソッドとヘッダ
		w.Header().Set("Access-Control-Allow-Methods", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")

		// 3. 認証情報（トークンなど）の送信を許可
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// OPTIONSメソッド（プリフライトリクエスト）の場合はここで200を返して終了
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		// それ以外の通常リクエストは次のハンドラ（ServeMux）へ渡す
		next.ServeHTTP(w, r)
	})
}
