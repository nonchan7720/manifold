package client

import (
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var (
	customTransport = http.DefaultTransport.(*http.Transport).Clone() //nolint: errcheck,forcetypeassert
)

func init() {
	setTransportSetting(customTransport)
}

func setTransportSetting(t *http.Transport) {
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = 100
	t.IdleConnTimeout = 45 * time.Second
}

func CustomTransport() *http.Transport {
	return customTransport.Clone()
}

func OTELTransport() *otelhttp.Transport {
	return otelhttp.NewTransport(CustomTransport())
}

func Transport() http.RoundTripper {
	return OTELTransport()
}

func HTTPClient() *http.Client {
	c := &http.Client{
		Transport: Transport(),
		Timeout:   10 * time.Second,
	}
	return c
}

func MergeTransport(rt http.RoundTripper) http.RoundTripper {
	switch t := rt.(type) { //nolint: gocritic
	case *http.Transport:
		setTransportSetting(t)
	}
	return rt
}
