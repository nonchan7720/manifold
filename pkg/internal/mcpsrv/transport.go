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
	var base http.RoundTripper
	switch {
	case auth != nil:
		// AuthValue が設定されている場合は API キー等の静的認証。
		// コンテキストのトークンは転送せず、AuthValue のヘッダーを付加する。
		// AuthValue が未設定の場合はコンテキストのトークンを転送する。
		base = client.NewAuthValueRoundTripper(client.Transport(), auth)
	case oauth2 != nil:
		// OAuth2.0（gateway が exchange したトークン）も
		// OAuth2.1（クライアントのトークンをそのまま）もこの経路を通る。
		base = client.NewOAuth2RoundTripper(client.Transport())
	case tokenExchange != nil:
		// API Key をトークン交換してOAuthトークンを利用する場合
		source := &client.InMemoryRegistry{}
		registry := client.NewBaseTokenRegistry(tokenExchange.URL, source)
		base = client.NewTokenExchangeRoundTrip(client.Transport(), registry)
	default:
		base = client.Transport()
	}
	return client.NewExtraHeaderRoundTripper(base, headers)
}
