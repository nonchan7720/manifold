package mcpsrv

import (
	"net/http"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
	goauth2 "golang.org/x/oauth2"
)

func httpClientRoundTripper(
	auth *config.AuthValue,
	oauth2 *config.OAuth2,
	tokenExchange *config.TokenExchange,
	googleSA *config.GoogleServiceAccount,
	googleURLs []string,
	headers map[string]string,
) (rt http.RoundTripper) {
	var registry client.TokenSource
	if tokenExchange != nil {
		registry = newTokenExchangeRegistry(tokenExchange)
	}
	var googleSource goauth2.TokenSource
	if googleSA != nil {
		googleSource = client.NewGoogleTokenSource(googleSA)
	}
	return newHTTPClientRoundTripper(
		client.Transport(),
		registry,
		googleSource,
		googleURLs,
		auth,
		oauth2,
		tokenExchange,
		headers,
	)
}

// newTokenExchangeRegistry は token exchange 用のトークンキャッシュ付きレジストリを作る。
func newTokenExchangeRegistry(tokenExchange *config.TokenExchange) client.TokenSource {
	return client.NewBaseTokenRegistry(tokenExchange.URL, &client.InMemoryBaseTokenRegister{})
}

// newHTTPClientRoundTripper は共有可能な base（コネクションプール）と registry
// （トークン交換のキャッシュ）の上に、設定に応じた認証ラッパーを重ねる。
// ラッパー自体は軽量で、呼び出し元ごとの認証情報は各リクエストの ctx から
// 注入されるため、base と registry を複数の呼び出しで共有しても安全。
func newHTTPClientRoundTripper(
	transport http.RoundTripper,
	registry client.TokenSource,
	googleSource goauth2.TokenSource,
	googleURLs []string,
	auth *config.AuthValue,
	oauth2 *config.OAuth2,
	tokenExchange *config.TokenExchange,
	headers map[string]string,
) http.RoundTripper {
	var base http.RoundTripper
	switch {
	case auth != nil:
		// AuthValue が設定されている場合は API キー等の静的認証。
		// コンテキストのトークンは転送せず、AuthValue のヘッダーを付加する。
		// AuthValue が未設定の場合はコンテキストのトークンを転送する。
		base = client.NewAuthValueRoundTripper(transport, auth)
	case tokenExchange != nil:
		// API Key をトークン交換してOAuthトークンを利用する場合
		if registry == nil {
			registry = newTokenExchangeRegistry(tokenExchange)
		}
		base = client.NewTokenExchangeRoundTrip(transport, registry)
	case googleSource != nil:
		// Google のアクセストークンを付加する（googleURLs のホストにのみ）。
		base = client.NewGoogleRoundTripper(transport, googleSource, googleURLs...)
	case oauth2 != nil:
		// OAuth2.0（gateway が exchange したトークン）も
		// OAuth2.1（クライアントのトークンをそのまま）もこの経路を通る。
		// oauth2 は OAuth2.1 Auto-Discovery で実行時に後付けされ得るため、設定ファイルで
		// 明示した認証（authValue / tokenExchange / googleServiceAccount）より後に判定する。
		// 先に判定すると、後付けされた oauth2 が明示した認証を黙って置き換えてしまう。
		base = client.NewOAuth2RoundTripper(transport)
	default:
		base = transport
	}
	return client.NewExtraHeaderRoundTripper(base, headers)
}
