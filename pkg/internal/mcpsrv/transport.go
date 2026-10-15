package mcpsrv

import (
	"net/http"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

func httpClientRoundTripper(
	auth *config.AuthValue,
	oauth2 *config.OAuth2,
	tokenExchange *config.TokenExchange,
	headers map[string]string,
) (rt http.RoundTripper) {
	var registry client.TokenSource
	if tokenExchange != nil {
		registry = newTokenExchangeRegistry(tokenExchange)
	}
	return newHTTPClientRoundTripper(
		client.Transport(),
		registry,
		auth,
		oauth2,
		tokenExchange,
		headers,
	)
}

// newTokenExchangeRegistry は token exchange 用のトークンキャッシュ付きレジストリを作る。
func newTokenExchangeRegistry(tokenExchange *config.TokenExchange) client.TokenSource {
	return client.NewBaseTokenRegistry(tokenExchange.URL, &client.InMemoryRegistry{})
}

// newHTTPClientRoundTripper は共有可能な base（コネクションプール）と registry
// （トークン交換のキャッシュ）の上に、設定に応じた認証ラッパーを重ねる。
// ラッパー自体は軽量で、呼び出し元ごとの認証情報は各リクエストの ctx から
// 注入されるため、base と registry を複数の呼び出しで共有しても安全。
func newHTTPClientRoundTripper(
	transport http.RoundTripper,
	registry client.TokenSource,
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
	case oauth2 != nil:
		// OAuth2.0（gateway が exchange したトークン）も
		// OAuth2.1（クライアントのトークンをそのまま）もこの経路を通る。
		base = client.NewOAuth2RoundTripper(transport)
	case tokenExchange != nil:
		// API Key をトークン交換してOAuthトークンを利用する場合
		if registry == nil {
			registry = newTokenExchangeRegistry(tokenExchange)
		}
		base = client.NewTokenExchangeRoundTrip(transport, registry)
	default:
		base = transport
	}
	return client.NewExtraHeaderRoundTripper(base, headers)
}
