package client

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// CloudAuth はクラウドベンダーのサービスアカウント / IAM 認証。バックエンドへ
// 出ていくリクエストに資格情報（Bearer トークンや署名）を付ける RoundTripper で
// base を包む。ベンダーを増やすときはこの interface を実装し、NewCloudAuth に
// 設定の分岐を足す。
//
// 実装はトークンや認証情報のキャッシュを持つので、サーバーごとに 1 つ作って
// 共有する（Wrap は何度呼んでもよい）。資格情報の読み込みは初回リクエストまで
// 遅延させ、失敗してもリクエストごとに再試行する。
type CloudAuth interface {
	Wrap(base http.RoundTripper) http.RoundTripper
}

// NewCloudAuth は設定されているクラウド認証を返す。どれも未設定なら nil。
// 設定の排他は config 側で検証済み。
func NewCloudAuth(g *config.GoogleServiceAccount, a *config.AWSIAM) CloudAuth {
	switch {
	case g != nil:
		return NewGoogleServiceAccountAuth(g)
	case a != nil:
		return NewAWSIAMAuth(a)
	default:
		return nil
	}
}

// GoogleServiceAccountAuth はサービスアカウント鍵から JWT bearer フローで
// アクセストークンを取得し、Authorization: Bearer を付ける。
type GoogleServiceAccountAuth struct {
	cfg *config.GoogleServiceAccount
	// tokenURL が空でなければ鍵 JSON の token_uri を上書きする（テスト用）。
	tokenURL string

	mu sync.Mutex
	ts oauth2.TokenSource
}

func NewGoogleServiceAccountAuth(cfg *config.GoogleServiceAccount) *GoogleServiceAccountAuth {
	return &GoogleServiceAccountAuth{cfg: cfg}
}

var _ CloudAuth = (*GoogleServiceAccountAuth)(nil)

func (g *GoogleServiceAccountAuth) tokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.ts != nil {
		return g.ts, nil
	}
	raw, err := g.cfg.LoadCredentials()
	if err != nil {
		return nil, err
	}
	jc, err := google.JWTConfigFromJSON(raw, g.cfg.Scopes...)
	if err != nil {
		return nil, err
	}
	if g.tokenURL != "" {
		jc.TokenURL = g.tokenURL
	}
	// TokenSource に渡す ctx は更新のたびに使われるので、リクエストのキャンセルから
	// 切り離す（最初のリクエストが終わるたびに更新が失敗しないようにする）。
	ctx = context.WithValue(context.WithoutCancel(ctx), oauth2.HTTPClient, HTTPClient())
	// JWT の TokenSource は有効期限が切れるたびに取り直す。ReuseTokenSource で
	// 期限内のトークンをキャッシュし、同時リクエストの更新も 1 回にまとめる。
	g.ts = oauth2.ReuseTokenSource(nil, jc.TokenSource(ctx))
	return g.ts, nil
}

func (g *GoogleServiceAccountAuth) Wrap(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &googleRoundTripper{base: base, auth: g}
}

type googleRoundTripper struct {
	base http.RoundTripper
	auth *GoogleServiceAccountAuth
}

func (rt *googleRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	ts, err := rt.auth.tokenSource(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load google service account credentials",
			logging.WithStackTrace(err)...)
		return errorResponse(
			http.StatusBadGateway,
			"google service account credentials unavailable",
		), nil
	}
	token, err := ts.Token()
	if err != nil {
		slog.ErrorContext(ctx, "failed to get google service account token",
			logging.WithStackTrace(err)...)
		return errorResponse(http.StatusBadGateway, "google service account token unavailable"), nil
	}
	req = req.Clone(ctx)
	req.Header.Set("Authorization", token.Type()+" "+token.AccessToken)
	return rt.base.RoundTrip(req)
}
