package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/internal/logging"
	"golang.org/x/oauth2"
)

type TokenSource interface {
	Get(key string) oauth2.TokenSource
}

// defaultRetryAfter は Retry-After ヘッダーが無い、または解釈できない場合に使う既定の待機時間。
const defaultRetryAfter = 30 * time.Second

// baseTokenSource.Token が返すエラーを分類するためのセンチネルエラー群。
// tokenExchangeRoundTrip.RoundTrip はこれらを errors.Is / errors.As で判定し、
// 失敗の種類に応じて異なる HTTP ステータスをクライアントに返す。
var (
	// errRateLimited はトークン交換エンドポイントが 429 を返した（または直近の 429 の
	// ネガティブキャッシュ期間中である）ことを表す。呼び出し元の問題ではなく一時的な
	// 過負荷なので 503 として扱う。
	errRateLimited = errors.New("token exchange rate limited")

	// errCredentialRejected はトークン交換エンドポイントが 401/403 を返したことを表す。
	// これは API キー自体が拒否されたことを意味するため 401 として扱う。
	errCredentialRejected = errors.New("token exchange credential rejected")

	// errUpstreamUnavailable はネットワークエラー、5xx などの想定外のステータス、
	// レスポンスのデコード失敗、リクエスト構築失敗など、呼び出し元にもトークン交換
	// エンドポイントの資格情報にも起因しない一時的な障害を表す。502 として扱う。
	errUpstreamUnavailable = errors.New("token exchange upstream unavailable")
)

// rateLimitedError は 429 によるトークン交換失敗を表す。RetryAfter が既知の場合、
// RoundTrip はこれをもとに Retry-After ヘッダーを付与する。
type rateLimitedError struct {
	msg        string
	retryAfter time.Duration
}

func (e *rateLimitedError) Error() string { return e.msg }

// Is により errors.Is(err, errRateLimited) が true になるようにする。
func (e *rateLimitedError) Is(target error) bool { return target == errRateLimited }

type baseTokenSource struct {
	client *http.Client
	url    string
	apiKey string

	// now はテスト時にクロックを差し替えられるようにするためのフック。nil の場合は time.Now を使う。
	now func() time.Time

	// mu は rateLimitedUntil / rateLimitMsg の読み書きを保護する。
	// 429 のレート制限ウィンドウ中は新規リクエストを発行せず、キャッシュしたエラーを
	// 即座に返すため、複数 goroutine から同時に参照・更新されうる。
	mu               sync.Mutex
	rateLimitedUntil time.Time
	rateLimitMsg     string
}

func (s *baseTokenSource) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *baseTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	until, cachedMsg := s.rateLimitedUntil, s.rateLimitMsg
	s.mu.Unlock()
	if cachedMsg != "" && s.clock().Before(until) {
		// レート制限ウィンドウ内なので、新規リクエストを発行せずキャッシュしたエラーを返す。
		return nil, &rateLimitedError{msg: cachedMsg, retryAfter: until.Sub(s.clock())}
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to build request: %w", errUpstreamUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to exchange request: %w", errUpstreamUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfterHeader := resp.Header.Get("Retry-After")
		retryAfter := parseRetryAfter(s.clock(), retryAfterHeader)
		var msg string
		if retryAfterHeader != "" {
			msg = fmt.Sprintf("rate limited, retry after %s", retryAfterHeader)
		} else {
			msg = "rate limited"
		}
		s.mu.Lock()
		s.rateLimitedUntil = s.clock().Add(retryAfter)
		s.rateLimitMsg = msg
		s.mu.Unlock()
		return nil, &rateLimitedError{msg: msg, retryAfter: retryAfter}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		// API キー自体が拒否された（トークン交換エンドポイントの資格情報エラー）。
		return nil, fmt.Errorf("%w: exchange failed: status: %d", errCredentialRejected, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: exchange failed: status: %d", errUpstreamUnavailable, resp.StatusCode)
	}
	var token oauth2.Token
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, fmt.Errorf("%w: failed to decode response: %w", errUpstreamUnavailable, err)
	}
	// RFC 8693 の expires_in（秒数）は Expiry に反映するのがアプリ側の責務
	// （golang.org/x/oauth2 の Token.ExpiresIn のドキュメント参照）。
	if token.ExpiresIn > 0 && token.Expiry.IsZero() {
		token.Expiry = s.clock().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	return &token, nil
}

// parseRetryAfter は Retry-After ヘッダーの値を待機時間に変換する。
// 整数（秒数）と HTTP-date のどちらの形式もサポートし、値が空、または解釈できない場合は
// defaultRetryAfter を返す。
func parseRetryAfter(now time.Time, v string) time.Duration {
	if v == "" {
		return defaultRetryAfter
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return defaultRetryAfter
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return defaultRetryAfter
}

type BaseTokenRegister interface {
	Get(apiKey string) (oauth2.TokenSource, bool)
	Set(apiKey string, ts oauth2.TokenSource) error
	// GetOrCreate は apiKey に対応する TokenSource を取得し、存在しない場合のみ create で
	// 作成・登録する。この取得と登録を単一のアトミック操作として行う実装を要求する
	// （実装は sync.Map.LoadOrStore 等を想定）。これにより baseTokenRegistry.Get の
	// Get→Set という非アトミックな手順に起因する、同一 apiKey への同時初回アクセスでの
	// TokenSource 重複生成を防ぐ。
	GetOrCreate(apiKey string, create func() oauth2.TokenSource) oauth2.TokenSource
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
	return r.sources.GetOrCreate(apiKey, func() oauth2.TokenSource {
		src := &baseTokenSource{apiKey: apiKey, client: r.client, url: r.url}
		return oauth2.ReuseTokenSource(nil, src)
	})
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
		return unauthorizedResponse("token is empty"), nil
	}
	token, err := rt.registry.Get(baseToken).Token()
	if err != nil {
		slog.ErrorContext(ctx, "failed to token exchange", logging.WithStackTrace(err)...)
		var rlErr *rateLimitedError
		switch {
		case errors.As(err, &rlErr):
			// トークン交換エンドポイントが 429（一時的な過負荷）を返した。
			// クライアントには 503 を返し、既知であれば Retry-After を付与する。
			resp := errorResponse(http.StatusServiceUnavailable, "token exchange rate limited")
			if rlErr.retryAfter > 0 {
				secs := int(rlErr.retryAfter.Round(time.Second) / time.Second)
				if secs < 1 {
					secs = 1
				}
				resp.Header.Set("Retry-After", strconv.Itoa(secs))
			}
			return resp, nil
		case errors.Is(err, errCredentialRejected):
			// API キー自体が拒否された。
			return unauthorizedResponse("failed to token exchange"), nil
		default:
			// ネットワークエラー、5xx、デコード失敗など、呼び出し元にもトークン交換の
			// 資格情報にも起因しない一時的な障害。
			return errorResponse(http.StatusBadGateway, "token exchange unavailable"), nil
		}
	}
	req = req.Clone(ctx)
	req.Header.Set("Authorization", token.Type()+" "+token.AccessToken)
	return rt.base.RoundTrip(req)
}
