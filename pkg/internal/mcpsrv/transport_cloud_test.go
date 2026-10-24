package mcpsrv

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

type stubCloudAuth struct{}

func (stubCloudAuth) Wrap(base http.RoundTripper) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer cloud-token")
		return base.RoundTrip(req)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestHTTPClientRoundTripper_CloudAuth(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
	}))
	t.Cleanup(backend.Close)

	rt := newHTTPClientRoundTripper(http.DefaultTransport, nil, nil, nil, nil, stubCloudAuth{}, nil)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, backend.URL, nil)
	require.NoError(t, err)
	resp, err := (&http.Client{Transport: rt}).Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, "Bearer cloud-token", got)
}
