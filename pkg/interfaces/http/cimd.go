package httphandler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/util"
)

// CIMD (Client ID Metadata Documents, SEP-991) 関連の実装。
// MCP 2025-11-25 仕様では、client_id として HTTPS URL を使用し、その URL から
// クライアントメタデータドキュメントを取得する登録方式が追加された。
// https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization#client-id-metadata-documents

const (
	// cimdCacheTTL は取得済み CIMD ドキュメントのキャッシュ期間。
	// メタデータの更新（redirect_uris の変更など）を短時間で反映できるよう控えめにする。
	cimdCacheTTL = 5 * time.Minute
	// cimdMaxBodySize は CIMD ドキュメント取得時のレスポンスボディ上限。
	cimdMaxBodySize = 1 << 20 // 1MB
	// cimdCachePrefix は store に保存する際のキープレフィックス。
	cimdCachePrefix = "cimd_doc:"
)

// ClientIDMetadataDocument は CIMD ドキュメント
// (draft-ietf-oauth-client-id-metadata-document) の内容を保持する。
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

// isCIMDClientID は client_id が CIMD の URL 形式かどうかを判定する。
// draft-ietf-oauth-client-id-metadata-document に従い、https スキームで
// パスを持つ URL のみを CIMD として扱う（DCR で発行したランダム ID と区別する）。
func isCIMDClientID(clientID string) bool {
	if !strings.HasPrefix(clientID, "https://") {
		return false
	}
	u, err := url.Parse(clientID)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && u.Host != "" && u.Path != "" && u.Path != "/" &&
		u.Fragment == "" && u.User == nil
}

// fetchClientIDMetadata は client_id URL から CIMD ドキュメントを取得・検証して返す。
// 検証済みドキュメントは store にキャッシュされる。
func (h *AuthHandler) fetchClientIDMetadata(ctx context.Context, clientID string) (_ *ClientIDMetadataDocument, rErr error) {
	ctx = trace.StartSpan(ctx, "httphandler/AuthHandler/fetchClientIDMetadata")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if !isCIMDClientID(clientID) {
		return nil, fmt.Errorf("client_id is not a valid client ID metadata document URL")
	}

	// キャッシュを確認
	if cached, err := h.store.Get(ctx, cimdCachePrefix+clientID); err == nil {
		var doc ClientIDMetadataDocument
		if err := json.Unmarshal([]byte(cached), &doc); err == nil {
			return &doc, nil
		}
	}

	httpClient := h.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	doc, err := fetchCIMDDocument(ctx, httpClient, clientID)
	if err != nil {
		return nil, err
	}
	if err := validateCIMDDocument(doc, clientID); err != nil {
		return nil, err
	}

	docJSON, err := json.Marshal(doc)
	if err == nil {
		if err := h.store.Set(ctx, cimdCachePrefix+clientID, docJSON, cimdCacheTTL); err != nil {
			slog.WarnContext(ctx, "failed to cache CIMD document",
				slog.String("client_id", util.SanitizeLog(clientID)), slog.Any("error", err))
		}
	}
	return doc, nil
}

// fetchCIMDDocument は client_id URL に GET リクエストを送り、
// CIMD ドキュメントを取得・パースして返す。内容の検証は行わない。
//
// CIMD は仕様上クライアントが提示した URL の取得が必須となる。SSRF 対策として
// isCIMDClientID で https スキームを強制し、本番では SafeHTTPClient
// （プライベート IP への接続拒否）を使用、さらにサイズ上限と Content-Type 検証を課す。
func fetchCIMDDocument(ctx context.Context, httpClient *http.Client, clientID string) (*ClientIDMetadataDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil) //nolint: gosec // G704: CIMD requires fetching the client-supplied URL; mitigated above
	if err != nil {
		return nil, fmt.Errorf("build CIMD request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req) //nolint: gosec // G704: see above
	if err != nil {
		return nil, fmt.Errorf("fetch CIMD document: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch CIMD document: unexpected status %d", resp.StatusCode)
	}
	if mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		return nil, fmt.Errorf("fetch CIMD document: content-type must be application/json")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read CIMD document: %w", err)
	}
	if len(body) > cimdMaxBodySize {
		return nil, fmt.Errorf("CIMD document exceeds size limit")
	}

	var doc ClientIDMetadataDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse CIMD document: %w", err)
	}
	return &doc, nil
}

// validateCIMDDocument は取得した CIMD ドキュメントの内容を検証する。
func validateCIMDDocument(doc *ClientIDMetadataDocument, clientID string) error {
	// draft に従い、ドキュメント内の client_id は取得元 URL と一致しなければならない
	if doc.ClientID != clientID {
		return fmt.Errorf("CIMD document client_id does not match document URL")
	}
	if len(doc.RedirectURIs) == 0 {
		return fmt.Errorf("CIMD document has no redirect_uris")
	}
	for _, uri := range doc.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			return fmt.Errorf("CIMD document has invalid redirect_uri: %w", err)
		}
	}
	// CIMD クライアントは秘密情報を検証できないためパブリッククライアントのみ許可
	if doc.TokenEndpointAuthMethod != "" && doc.TokenEndpointAuthMethod != "none" {
		return fmt.Errorf("CIMD client must use token_endpoint_auth_method \"none\"")
	}
	return nil
}

// serverNameFromResource は RFC 8707 の resource パラメータ
// （例: https://host/mcp/{name}）から MCP サーバー名を導出する。
// 解決できない場合は空文字を返す。
func serverNameFromResource(resource string) string {
	u, err := url.Parse(resource)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 2 && parts[0] == "mcp" && parts[1] != "" {
		return parts[1]
	}
	return ""
}

// clientMetadataDocumentURL は manifold 自身が上流認可サーバーに提示する
// CIMD ドキュメントの URL を返す。CIMD の client_id は https でなければ
// ならないため、ゲートウェイが https で公開されていない場合は空文字を返す。
func clientMetadataDocumentURL(gatewayBaseURL, serverName string) string {
	u, err := url.Parse(gatewayBaseURL)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	return fmt.Sprintf("%s/%s/auth/client-metadata.json", gatewayBaseURL, serverName)
}

// ClientMetadataDocument は GET /{server}/auth/client-metadata.json を処理し、
// manifold 自身の CIMD ドキュメントを配信する。上流認可サーバーが CIMD に
// 対応している場合、manifold はこの URL を client_id として使用する。
func (h *AuthHandler) ClientMetadataDocument(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	ctx := r.Context()
	ctx = trace.StartSpan(ctx, "httphandler/AuthHandler/ClientMetadataDocument")
	defer func() { trace.EndSpan(ctx, nil) }()

	if srv == nil {
		http.NotFound(w, r)
		return
	}

	baseURL := h.getBaseURL(r)
	docURL := fmt.Sprintf("%s/%s/auth/client-metadata.json", baseURL, srv.Name)
	callbackURL := fmt.Sprintf("%s/%s/auth/callback", baseURL, srv.Name)
	doc := ClientIDMetadataDocument{
		ClientID:                docURL,
		ClientName:              "manifold",
		ClientURI:               baseURL,
		RedirectURIs:            []string{callbackURL},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		TokenEndpointAuthMethod: "none",
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_ = json.NewEncoder(w).Encode(doc)
}
