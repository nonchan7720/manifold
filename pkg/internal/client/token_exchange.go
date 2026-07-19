package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
	"golang.org/x/oauth2"
)

type TokenSource interface {
	Get(key string) oauth2.TokenSource
}

type baseTokenSource struct {
	client *http.Client
	url    string
	apiKey string
}

func (s *baseTokenSource) Token() (*oauth2.Token, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusTooManyRequests {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			return nil, fmt.Errorf("rate limited, retry after %s", retryAfter)
		}
		return nil, fmt.Errorf("rate limited")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("exchange failed: status: %d", resp.StatusCode)
	}
	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &token, nil
}

type BaseTokenRegister interface {
	Get(apiKey string) (oauth2.TokenSource, bool)
	Set(apiKey string, ts oauth2.TokenSource) error
}

type baseTokenRegistry struct {
	url     string
	sources BaseTokenRegister
	client  *http.Client
}

func NewBaseTokenRegistry(url string, sources BaseTokenRegister) TokenSource {
	return &baseTokenRegistry{
		url:     url,
		client:  HTTPClient(),
		sources: sources,
	}
}

func (r *baseTokenRegistry) Get(apiKey string) oauth2.TokenSource {
	if ts, ok := r.sources.Get(apiKey); ok {
		return ts
	}
	src := &baseTokenSource{apiKey: apiKey, client: r.client, url: r.url}
	ts := oauth2.ReuseTokenSource(nil, src)
	r.sources.Set(apiKey, ts)
	return ts
}

type tokenExchangeRoundTrip struct {
	base     http.RoundTripper
	registry TokenSource
}

var _ http.RoundTripper = (*tokenExchangeRoundTrip)(nil)

func NewTokenExchangeRoundTrip(base http.RoundTripper, registry TokenSource) http.RoundTripper {
	if base == nil {
		base = Transport()
	}
	return &tokenExchangeRoundTrip{
		base:     base,
		registry: registry,
	}
}

func (rt *tokenExchangeRoundTrip) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	baseToken := contexts.FromRequestAuthHeader(ctx)
	if baseToken == "" {
		return &http.Response{
			Status:     http.StatusText(http.StatusUnauthorized),
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"token is empty"}`))),
		}, nil
	}
	token, err := rt.registry.Get(baseToken).Token()
	if err != nil {
		slog.ErrorContext(ctx, "failed to token exchange", logging.WithStackTrace(err)...)
		resp := &http.Response{
			Status:     http.StatusText(http.StatusUnauthorized),
			StatusCode: http.StatusUnauthorized,
			Body:       io.NopCloser(bytes.NewReader([]byte(`{"error":"failed to token exchange"}`))),
		}
		return resp, nil
	}
	req = req.Clone(ctx)
	req.Header.Set("Authorization", token.Type()+" "+token.AccessToken)
	return rt.base.RoundTrip(req)
}
