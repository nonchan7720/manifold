package httphandler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/nonchan7720/manifold/pkg/util"
)

// OAuth Client ID Metadata Document (draft-ietf-oauth-client-id-metadata-document)。
// client_id が HTTPS URL のとき、その URL からクライアントメタデータを取得し、
// 動的クライアント登録なしにクライアントを受け入れる。

const (
	// dcrClientKeyPrefix は動的クライアント登録の結果を保存する store キーの接頭辞。
	dcrClientKeyPrefix = "oauth_client:"
	// cimdClientKeyPrefix は解決済み CIMD クライアントを保存する store キーの接頭辞。
	cimdClientKeyPrefix = "cimd_client:"

	// cimdFetchTimeout はメタデータドキュメント取得の最大待ち時間。
	cimdFetchTimeout = 10 * time.Second
)

// StoreClientRegistration.Source が取る値。
const (
	ClientSourceDCR  = "dcr"
	ClientSourceCIMD = "cimd"
)

// errInvalidClient はクライアント側に起因する解決失敗を表す。呼び出し側は
// これを invalid_client（401）に、それ以外を内部エラーに対応付ける。
var errInvalidClient = errors.New("invalid_client")

// ClientIDMetadataDocument は client_id URL が返すクライアントメタデータ。
type ClientIDMetadataDocument struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name,omitempty"`
	ClientURI               string   `json:"client_uri,omitempty"`
	LogoURI                 string   `json:"logo_uri,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
}

// cimdClientCacheKey は client_id をそのまま store キーに含めないための
// ハッシュ済みキーを返す。client_id は URL でありキーに使えない文字を含む。
func cimdClientCacheKey(clientID string) string {
	sum := sha256.Sum256([]byte(clientID))
	return cimdClientKeyPrefix + hex.EncodeToString(sum[:])
}

// validateCIMDClientID はメタデータドキュメントを取得する前に client_id URL を検査する。
func validateCIMDClientID(clientID string, cfg config.CIMDConfig) error {
	u, err := url.Parse(clientID)
	if err != nil {
		return fmt.Errorf("%w: client_id is not a URL", errInvalidClient)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: client_id must use https", errInvalidClient)
	}
	if u.Host == "" || u.Path == "" || u.Path == "/" {
		return fmt.Errorf("%w: client_id must have a host and a path", errInvalidClient)
	}
	if u.Fragment != "" || u.User != nil {
		return fmt.Errorf("%w: client_id must not contain a fragment or userinfo", errInvalidClient)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil || strings.EqualFold(host, "localhost") {
		return fmt.Errorf("%w: client_id host must be a public host name", errInvalidClient)
	}
	if !cfg.AllowsOrigin(u.Scheme + "://" + u.Host) {
		return fmt.Errorf("%w: client_id origin is not allowed", errInvalidClient)
	}
	return nil
}

// validateCIMDDocument は取得したメタデータドキュメントの内容を検査する。
// client_id は正規化せず文字列として完全一致を要求する。
func validateCIMDDocument(doc *ClientIDMetadataDocument, clientID string) error {
	if doc.ClientID != clientID {
		return fmt.Errorf(
			"%w: document client_id does not match the requested one",
			errInvalidClient,
		)
	}
	if len(doc.RedirectURIs) == 0 {
		return fmt.Errorf("%w: document has no redirect_uris", errInvalidClient)
	}
	for _, uri := range doc.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			return fmt.Errorf("%w: document has an invalid redirect_uri: %w", errInvalidClient, err)
		}
	}
	// CIMD クライアントはシークレットを保持できないためパブリッククライアントのみ受け入れる。
	if doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != "none" {
		return fmt.Errorf("%w: token_endpoint_auth_method must be \"none\"", errInvalidClient)
	}
	if len(doc.GrantTypes) > 0 && !slices.Contains(doc.GrantTypes, "authorization_code") {
		return fmt.Errorf("%w: grant_types must include authorization_code", errInvalidClient)
	}
	return nil
}

// cimdCacheTTL は Cache-Control の max-age と設定値の小さい方を返す。
// no-store / no-cache が付いていればキャッシュしない（0）。
func cimdCacheTTL(cacheControl string, configured time.Duration) time.Duration {
	for directive := range strings.SplitSeq(cacheControl, ",") {
		d := strings.ToLower(strings.TrimSpace(directive))
		if d == "no-store" || d == "no-cache" {
			return 0
		}
		value, ok := strings.CutPrefix(d, "max-age=")
		if !ok {
			continue
		}
		secs, err := strconv.Atoi(strings.Trim(value, `"`))
		if err != nil || secs < 0 {
			continue
		}
		if maxAge := time.Duration(secs) * time.Second; maxAge < configured {
			return maxAge
		}
	}
	return configured
}

// fetchCIMDDocument は client_id URL からメタデータドキュメントを取得する。
// リダイレクトは追従しない: 初回 URL が https でも転送先で http や別ホストへ
// 移り得るため、client_id URL から直接取得できる場合のみ受け付ける。
func fetchCIMDDocument(
	ctx context.Context,
	httpClient *http.Client,
	clientID string,
	cfg config.CIMDConfig,
) (*ClientIDMetadataDocument, time.Duration, error) {
	req, err := http.NewRequestWithContext( //nolint: gosec // G704: CIMD は client 提示の URL 取得が前提。validateCIMDClientID と SafeHTTPClient で緩和する
		ctx,
		http.MethodGet,
		clientID,
		nil,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: build request: %w", errInvalidClient, err)
	}
	req.Header.Set("Accept", "application/json")

	c := *httpClient
	c.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	// codeql[go/request-forgery]
	resp, err := c.Do(req) //nolint: gosec // G704: 上記と同じ
	if err != nil {
		return nil, 0, fmt.Errorf("%w: fetch document: %w", errInvalidClient, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("%w: unexpected status %d", errInvalidClient, resp.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, 0, fmt.Errorf("%w: content-type must be application/json", errInvalidClient)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxDocumentSize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: read document: %w", errInvalidClient, err)
	}
	if int64(len(body)) > cfg.MaxDocumentSize {
		return nil, 0, fmt.Errorf("%w: document exceeds maxDocumentSize", errInvalidClient)
	}

	var doc ClientIDMetadataDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, 0, fmt.Errorf("%w: parse document: %w", errInvalidClient, err)
	}
	return &doc, cimdCacheTTL(resp.Header.Get("Cache-Control"), cfg.CacheTTL), nil
}

// resolveCIMDClient は client_id URL からクライアント登録を組み立てる。
// MCPServerName は空のままにする（CIMD クライアントは MCP サーバー横断で使える）。
func (h *AuthHandler) resolveCIMDClient(
	ctx context.Context,
	clientID string,
) (*StoreClientRegistration, error) {
	if err := validateCIMDClientID(clientID, h.cimd); err != nil {
		return nil, err
	}
	cacheKey := cimdClientCacheKey(clientID)
	if cached, err := h.store.Get(ctx, cacheKey); err == nil {
		var reg StoreClientRegistration
		if err := json.Unmarshal([]byte(cached), &reg); err == nil {
			return &reg, nil
		}
	}

	fetchCtx, cancel := context.WithTimeout(ctx, cimdFetchTimeout)
	defer cancel()
	doc, ttl, err := fetchCIMDDocument(fetchCtx, h.httpClient, clientID, h.cimd)
	if err != nil {
		return nil, err
	}
	if err := validateCIMDDocument(doc, clientID); err != nil {
		return nil, err
	}

	reg := &StoreClientRegistration{
		ClientID:                doc.ClientID,
		ClientIDIssuedAt:        time.Now().Unix(),
		RedirectURIs:            doc.RedirectURIs,
		GrantTypes:              doc.GrantTypes,
		ResponseTypes:           doc.ResponseTypes,
		ClientName:              doc.ClientName,
		TokenEndpointAuthMethod: doc.TokenEndpointAuthMethod,
		Source:                  ClientSourceCIMD,
	}
	if ttl > 0 {
		h.cacheCIMDClient(ctx, cacheKey, clientID, reg, ttl)
	}
	return reg, nil
}

func (h *AuthHandler) cacheCIMDClient(
	ctx context.Context,
	cacheKey, clientID string,
	reg *StoreClientRegistration,
	ttl time.Duration,
) {
	regJSON, err := json.Marshal(reg)
	if err == nil {
		err = h.store.Set(ctx, cacheKey, regJSON, ttl)
	}
	if err != nil {
		slog.WarnContext(ctx, "failed to cache CIMD client",
			slog.String("client_id", util.SanitizeLog(clientID)),
			slog.Any("error", err))
	}
}

// resolveClient は下流の client_id をクライアント登録に解決する。
// 動的クライアント登録済みのクライアントを優先し、store に無い場合のみ
// CIMD として解決する。
func (h *AuthHandler) resolveClient(
	ctx context.Context,
	clientID string,
) (_ *StoreClientRegistration, rErr error) {
	ctx = trace.StartSpan(ctx, "httphandler/AuthHandler/resolveClient")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if clientID == "" {
		return nil, fmt.Errorf("%w: client_id is empty", errInvalidClient)
	}
	clientJSON, err := h.store.Get(ctx, dcrClientKeyPrefix+clientID)
	switch {
	case err == nil:
		var reg StoreClientRegistration
		if err := json.Unmarshal([]byte(clientJSON), &reg); err != nil {
			return nil, fmt.Errorf("unmarshal client registration: %w", err)
		}
		if reg.Source == "" {
			reg.Source = ClientSourceDCR
		}
		return &reg, nil
	case !errors.Is(err, store.ErrNotFound):
		// バックエンド障害はクライアント起因ではないので errInvalidClient で
		// ラップせずそのまま返し、呼び出し側に内部エラーとして扱わせる。
		// ここで CIMD へフォールバックすると、store が落ちているだけの状態が
		// 「登録されていないクライアント」として 401 で返ってしまう。
		return nil, fmt.Errorf("look up client registration: %w", err)
	}
	if !h.cimd.Enabled {
		return nil, fmt.Errorf("%w: client_id is not registered", errInvalidClient)
	}
	return h.resolveCIMDClient(ctx, clientID)
}
