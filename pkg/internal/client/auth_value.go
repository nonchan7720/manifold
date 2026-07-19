package client

import (
	"net/http"

	"github.com/nonchan7720/manifold/pkg/config"
)

type authValueRoundTripper struct {
	base      http.RoundTripper
	authValue *config.AuthValue
}

func NewAuthValueRoundTripper(base http.RoundTripper, auth *config.AuthValue) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &authValueRoundTripper{
		base:      base,
		authValue: auth,
	}
}

func (t *authValueRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// authValue が未設定の場合は何も付加せずそのまま転送する
	// （extraHeaderRoundTripper の nil-safety と同じ扱い）。
	if t.authValue == nil {
		return t.base.RoundTrip(req)
	}
	req = req.Clone(req.Context())
	value := t.authValue.Value
	if t.authValue.Prefix != "" {
		value = t.authValue.Prefix + " " + value
	}
	req.Header.Set(t.authValue.Header, value)
	return t.base.RoundTrip(req)
}
