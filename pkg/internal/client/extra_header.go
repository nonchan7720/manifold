package client

import (
	"net/http"
)

type extraHeaderRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func NewExtraHeaderRoundTripper(base http.RoundTripper, headers map[string]string) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &extraHeaderRoundTripper{
		base:    base,
		headers: headers,
	}
}

func (t *extraHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if len(t.headers) > 0 {
		req = req.Clone(req.Context())
		for k, v := range t.headers {
			req.Header.Set(k, v)
		}
	}
	return t.base.RoundTrip(req)
}
