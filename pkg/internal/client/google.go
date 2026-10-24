package client

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/nonchan7720/manifold/pkg/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// googleTokenSource は、最初の Token 呼び出しでサービスアカウントキーを読んで
// キャッシュ・自動更新付きのトークンソースを作る。キーの不備は設定ロード時に検証
// 済みで、ここでは（ロード後にキーのファイルが消えた場合など）リクエストの失敗として
// 返す。
type googleTokenSource struct {
	cfg *config.GoogleServiceAccount

	once   sync.Once
	source oauth2.TokenSource
	err    error
}

// NewGoogleTokenSource は Google サービスアカウントキーから、キャッシュ・自動更新付きの
// アクセストークンのソースを作る。トークンの取得先はキーの token_uri。
// ソースは呼び出しをまたいで共有する想定で、バックエンドごとに 1 つ作る。
func NewGoogleTokenSource(cfg *config.GoogleServiceAccount) oauth2.TokenSource {
	return &googleTokenSource{cfg: cfg}
}

func (s *googleTokenSource) Token() (*oauth2.Token, error) {
	s.once.Do(func() {
		raw, err := s.cfg.Credentials()
		if err != nil {
			s.err = err
			return
		}
		jwtCfg, err := google.JWTConfigFromJSON(raw, s.cfg.Scopes...)
		if err != nil {
			s.err = fmt.Errorf("invalid service account credentials: %w", err)
			return
		}
		// トークン取得はリクエスト ctx に紐づけず、ソースの寿命で使う ctx を渡す。
		ctx := context.WithValue(context.Background(), oauth2.HTTPClient, HTTPClient())
		s.source = oauth2.ReuseTokenSource(nil, jwtCfg.TokenSource(ctx))
	})
	if s.err != nil {
		return nil, s.err
	}
	return s.source.Token()
}

type googleRoundTripper struct {
	base   http.RoundTripper
	source oauth2.TokenSource
}

// NewGoogleRoundTripper は source のアクセストークンを Authorization: Bearer として
// 付ける。呼び出し元の資格情報は使わず転送もしない（静的な認証の authValue と同じ扱い）。
func NewGoogleRoundTripper(base http.RoundTripper, source oauth2.TokenSource) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &googleRoundTripper{base: base, source: source}
}

func (t *googleRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	token, err := t.source.Token()
	if err != nil {
		// RoundTripper はリクエストの body を閉じる責務がある。
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("google service account token: %w", err)
	}
	req = req.Clone(req.Context())
	token.SetAuthHeader(req)
	return t.base.RoundTrip(req)
}
