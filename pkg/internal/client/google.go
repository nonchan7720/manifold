package client

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/nonchan7720/manifold/pkg/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// googleTokenSource は、最初の Token 呼び出しでサービスアカウントキーを読む（キーが
// 無ければ ADC を解決する）。キャッシュ・自動更新付きのトークンソースを作る。キーの
// 不備は設定ロード時に検証済みで、ここでは（ロード後にキーのファイルが消えた場合や
// ADC が見つからない場合など）リクエストの失敗として返す。
type googleTokenSource struct {
	cfg *config.GoogleServiceAccount

	mu     sync.Mutex
	source oauth2.TokenSource
}

// NewGoogleTokenSource は Google サービスアカウントキー（無ければ ADC）から、
// キャッシュ・自動更新付きのアクセストークンのソースを作る。
// トークンの取得先はキーの token_uri（ADC では資格情報の種類による）。
// ソースは呼び出しをまたいで共有する想定で、バックエンドごとに 1 つ作る。
func NewGoogleTokenSource(cfg *config.GoogleServiceAccount) oauth2.TokenSource {
	return &googleTokenSource{cfg: cfg}
}

func (s *googleTokenSource) Token() (*oauth2.Token, error) {
	source, err := s.resolve()
	if err != nil {
		return nil, err
	}
	return source.Token()
}

// resolve は成功したトークンソースだけをキャッシュする。失敗は次の呼び出しで
// 再試行する（Pod 起動直後にメタデータサーバーが応答しない場合などの一時的な失敗で
// 再起動まで回復しなくなるのを防ぐ）。
func (s *googleTokenSource) resolve() (oauth2.TokenSource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.source != nil {
		return s.source, nil
	}
	// トークン取得はリクエスト ctx に紐づけず、ソースの寿命で使う ctx を渡す。
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, HTTPClient())
	if s.cfg.UseADC() {
		// ADC の解決（メタデータサーバー等）は設定ロード時ではなく、ここで遅延して行う。
		creds, err := google.FindDefaultCredentials(ctx, s.cfg.Scopes...)
		if err != nil {
			return nil, fmt.Errorf("google application default credentials: %w", err)
		}
		s.source = oauth2.ReuseTokenSource(nil, creds.TokenSource)
		return s.source, nil
	}
	raw, err := s.cfg.Credentials()
	if err != nil {
		return nil, err
	}
	jwtCfg, err := google.JWTConfigFromJSON(raw, s.cfg.Scopes...)
	if err != nil {
		return nil, fmt.Errorf("invalid service account credentials: %w", err)
	}
	s.source = oauth2.ReuseTokenSource(nil, jwtCfg.TokenSource(ctx))
	return s.source, nil
}

type googleRoundTripper struct {
	base   http.RoundTripper
	source oauth2.TokenSource
	// hosts はトークンを送ってよいホスト（ホスト名 + 実効ポート）。空なら固定しない。
	hosts map[string]struct{}
}

// NewGoogleRoundTripper は source のアクセストークンを Authorization: Bearer として
// 付ける。呼び出し元の資格情報は使わず転送もしない（静的な認証の authValue と同じ扱い）。
//
// トークンの漏えいを防ぐため、次の場合は付けない。
//   - https 以外（loopback の http を除く）: リクエストを拒否する。
//   - allowedURLs のホスト以外（バックエンドのリダイレクト先など）: Authorization を
//     外してそのまま転送する。リダイレクトの各ホップは RoundTripper を通るため、
//     ここで固定しないと別ホストへトークンが渡る。拒否ではなく外すのは、公開
//     ストレージの署名付き URL へのリダイレクトなど正当な用途を壊さないため。
//
// allowedURLs が空（ホストを特定できない）なら https のチェックのみ行う。
func NewGoogleRoundTripper(
	base http.RoundTripper, source oauth2.TokenSource, allowedURLs ...string,
) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	t := &googleRoundTripper{base: base, source: source}
	for _, raw := range allowedURLs {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		if t.hosts == nil {
			t.hosts = map[string]struct{}{}
		}
		t.hosts[hostKey(u)] = struct{}{}
	}
	return t
}

// hostKey はホスト名（小文字）と実効ポートを返す。
func hostKey(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

func (t *googleRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.hosts != nil {
		if _, ok := t.hosts[hostKey(req.URL)]; !ok {
			// 対象外のホストにはトークンも、呼び出し元由来の Authorization も送らない。
			req = req.Clone(req.Context())
			req.Header.Del("Authorization")
			return t.base.RoundTrip(req)
		}
	}
	if req.URL.Scheme != "https" && !config.IsLoopbackHost(req.URL.Hostname()) {
		// RoundTripper はリクエストの body を閉じる責務がある。
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf(
			"google service account token is only sent over https (got %q for host %q)",
			req.URL.Scheme, req.URL.Hostname(),
		)
	}
	token, err := t.source.Token()
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, fmt.Errorf("google service account token: %w", err)
	}
	req = req.Clone(req.Context())
	token.SetAuthHeader(req)
	return t.base.RoundTrip(req)
}
