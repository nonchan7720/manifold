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
	req = req.Clone(req.Context())
	value := t.authValue.Value
	if t.authValue.Prefix != "" {
		value = t.authValue.Prefix + " " + value
	}
	req.Header.Set(t.authValue.Header, value)
	return t.base.RoundTrip(req)
}
