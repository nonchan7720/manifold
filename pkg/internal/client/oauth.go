package client

import (
	"net/http"
	"strings"

	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// oauth2RoundTripper は middleware 層で解決されている OAuth2トークンをそのまま転送する
type oauth2RoundTripper struct {
	base http.RoundTripper
}

func NewOAuth2RoundTripper(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &oauth2RoundTripper{
		base: base,
	}
}

func (rt *oauth2RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	token := contexts.FromRequestAuthHeader(ctx)
	if token == "" {
		return unauthorizedResponse("token is empty"), nil
	}
	bearerToken := strings.TrimPrefix(token, "Bearer ")
	req = req.Clone(ctx)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	return rt.base.RoundTrip(req)
}
