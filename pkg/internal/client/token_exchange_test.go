package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// --- baseTokenSource.Token: Expiry の補完 ---

func TestBaseTokenSource_Token_SetsExpiryFromExpiresIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	src := &baseTokenSource{apiKey: "key", client: srv.Client(), url: srv.URL}
	before := time.Now()
	token, err := src.Token()
	require.NoError(t, err)
	require.Equal(t, "tok", token.AccessToken)
	require.False(t, token.Expiry.IsZero(), "Expiry should be populated from expires_in")
	require.WithinDuration(t, before.Add(3600*time.Second), token.Expiry, 5*time.Second)
}

func TestBaseTokenSource_Token_KeepsExplicitExpiry(t *testing.T) {
	explicitExpiry := time.Now().Add(10 * time.Minute).UTC().Truncate(time.Second)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"expiry":       explicitExpiry.Format(time.RFC3339),
		})
	}))
	defer srv.Close()

	src := &baseTokenSource{apiKey: "key", client: srv.Client(), url: srv.URL}
	token, err := src.Token()
	require.NoError(t, err)
	require.True(t, token.Expiry.Equal(explicitExpiry), "explicit expiry from response should not be overridden")
}

func TestBaseTokenSource_Token_NoExpiresIn_NoExpirySet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "tok",
			"token_type":   "Bearer",
		})
	}))
	defer srv.Close()

	src := &baseTokenSource{apiKey: "key", client: srv.Client(), url: srv.URL}
	token, err := src.Token()
	require.NoError(t, err)
	require.True(t, token.Expiry.IsZero())
}

// --- baseTokenSource.Token: 429 Retry-After バックオフ + ネガティブキャッシュ ---

func TestBaseTokenSource_Token_429_RetryAfterSeconds_SuppressesImmediateRetry(t *testing.T) {
	var requestCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	now := time.Now()
	src := &baseTokenSource{
		apiKey: "key",
		client: srv.Client(),
		url:    srv.URL,
		now:    func() time.Time { return now },
	}

	_, err := src.Token()
	require.Error(t, err)
	require.EqualValues(t, 1, requestCount.Load())

	// レート制限ウィンドウ内: サーバーに新規リクエストを発行せず、キャッシュしたエラーを即座に返す
	_, err2 := src.Token()
	require.Error(t, err2)
	require.EqualValues(t, 1, requestCount.Load(), "second call within the rate-limit window must not hit the server")
	require.Equal(t, err.Error(), err2.Error())
}

func TestBaseTokenSource_Token_429_WindowExpires_IssuesNewRequest(t *testing.T) {
	var requestCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	now := time.Now()
	src := &baseTokenSource{
		apiKey: "key",
		client: srv.Client(),
		url:    srv.URL,
		now:    func() time.Time { return now },
	}

	_, err := src.Token()
	require.Error(t, err)
	require.EqualValues(t, 1, requestCount.Load())

	// ウィンドウ内はまだリクエストを発行しない
	_, err = src.Token()
	require.Error(t, err)
	require.EqualValues(t, 1, requestCount.Load())

	// 疑似クロックを進め、レート制限ウィンドウを過ぎさせる
	now = now.Add(6 * time.Second)
	_, err = src.Token()
	require.Error(t, err)
	require.EqualValues(t, 2, requestCount.Load(), "after the window passes a new request should be issued")
}

func TestBaseTokenSource_Token_Success_NoRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok"})
	}))
	defer srv.Close()

	src := &baseTokenSource{apiKey: "key", client: srv.Client(), url: srv.URL}
	token, err := src.Token()
	require.NoError(t, err)
	require.Equal(t, "tok", token.AccessToken)
}

func TestBaseTokenSource_Token_OtherErrorStatus_NotCached(t *testing.T) {
	var requestCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	src := &baseTokenSource{apiKey: "key", client: srv.Client(), url: srv.URL}
	_, err := src.Token()
	require.Error(t, err)
	// 429以外のエラーはネガティブキャッシュされないので、毎回サーバーにリクエストが飛ぶ
	_, err = src.Token()
	require.Error(t, err)
	require.EqualValues(t, 2, requestCount.Load())
}

// --- parseRetryAfter ---

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{name: "empty uses default", in: "", want: defaultRetryAfter},
		{name: "seconds", in: "10", want: 10 * time.Second},
		{name: "negative seconds uses default", in: "-1", want: defaultRetryAfter},
		{name: "unparseable uses default", in: "not-a-duration", want: defaultRetryAfter},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(now, tt.in)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	now := time.Now()
	future := now.Add(15 * time.Second)
	got := parseRetryAfter(now, future.UTC().Format(http.TimeFormat))
	require.InDelta(t, 15*time.Second, got, float64(2*time.Second))
}

// --- baseTokenRegistry.Get: アトミックな get-or-create の利用 ---

func TestBaseTokenRegistry_Get_ReturnsSameTokenSourceForSameKey(t *testing.T) {
	source := &InMemoryRegistry{}
	registry := NewBaseTokenRegistry("http://example.com/token", source)

	baseRegistry, ok := registry.(*baseTokenRegistry)
	require.True(t, ok)

	ts1 := baseRegistry.Get("api-key")
	ts2 := baseRegistry.Get("api-key")
	require.Same(t, ts1, ts2)
}

func TestBaseTokenRegistry_Get_ConcurrentSameKey_NoDuplication(t *testing.T) {
	source := &InMemoryRegistry{}
	registry := NewBaseTokenRegistry("http://example.com/token", source)
	baseRegistry, ok := registry.(*baseTokenRegistry)
	require.True(t, ok)

	const goroutines = 50
	results := make([]oauth2.TokenSource, goroutines)
	var wg sync.WaitGroup
	var startWg sync.WaitGroup
	startWg.Add(1)

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			startWg.Wait()
			results[i] = baseRegistry.Get("shared")
		}(i)
	}
	startWg.Done()
	wg.Wait()

	first := results[0]
	require.NotNil(t, first)
	for i, got := range results {
		require.Same(t, first, got, "goroutine %d should observe the same TokenSource instance", i)
	}
}
