package httphandler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/redis"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/util"
	"golang.org/x/oauth2"
)

// AuthSession 進行中の認証セッションのために、Redisに保存されたデータを保持。
type AuthSession struct {
	ClientID            string `json:"client_id,omitempty"`
	RedirectURI         string `json:"redirect_uri,omitempty"`
	State               string `json:"state,omitempty"`
	CodeChallenge       string `json:"code_challenge,omitempty"`
	CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
	Resource            string `json:"resource,omitempty"` // RFC 8707
	// アップストリームのOAuth2設定のスナップショット（コールバック時に必要）
	OAuth2ClientID     string   `json:"oauth2_client_id,omitempty"`
	OAuth2ClientSecret string   `json:"oauth2_client_secret,omitempty"`
	OAuth2TokenURL     string   `json:"oauth2_token_url,omitempty"`
	OAuth2Scopes       []string `json:"oauth2_scopes,omitempty"`
	// 上流認可サーバーへのリクエストで使用した PKCE code_verifier
	UpstreamCodeVerifier string `json:"upstream_code_verifier,omitempty"`
}

// AuthCodeData 認証コードとトークンの交換に関するデータを保持。
type AuthCodeData struct {
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource,omitempty"`
	UpstreamTokenKey    string `json:"upstream_token_key"` // アップストリーム・トークンのRedisキー
}

// ClientRegistration 動的に登録された OAuth 2.0 クライアント（RFC 7591）を保持。
type ClientRegistration struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name,omitempty"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
}

// AuthHandler CLIおよびMCPクライアントの両方に対して、OAuth 2.1認証サーバーを実装。
type AuthHandler struct {
	redisClient *redis.Client
	servers     config.Servers
	mu          sync.RWMutex // servers への OAuth2 発見結果の書き込みを保護
}

func NewAuthHandler(redisClient *redis.Client, servers config.Servers) *AuthHandler {
	return &AuthHandler{
		redisClient: redisClient,
		servers:     servers,
	}
}

func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux, pathServerName string, middleware func(h http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc(fmt.Sprintf("GET /.well-known/oauth-protected-resource/mcp/{%s}", pathServerName), middleware(wrapMCPServer(h.OauthProtectedResource)))
	mux.HandleFunc(fmt.Sprintf("GET /.well-known/oauth-authorization-server/mcp/{%s}", pathServerName), middleware(wrapMCPServer(h.MetadataEndpoint)))
	mux.HandleFunc(fmt.Sprintf("GET /{%s}/auth/login", pathServerName), middleware(wrapMCPServer(h.LoginEndpoint)))
	mux.HandleFunc(fmt.Sprintf("GET /{%s}/auth/callback", pathServerName), middleware(wrapMCPServer(h.CallbackEndpoint)))
	mux.HandleFunc(fmt.Sprintf("POST /{%s}/auth/token", pathServerName), middleware(wrapMCPServer(h.TokenEndpoint)))
	// Dynamic Client Registration (RFC 7591)
	mux.HandleFunc(fmt.Sprintf("POST /{%s}/auth/clients", pathServerName), h.RegisterClientEndpoint)
}

func wrapMCPServer(next func(w http.ResponseWriter, r *http.Request, srv *config.Server)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		srvCfg := contexts.FromServerContext(r.Context())
		next(w, r, srvCfg)
	}
}

func (h *AuthHandler) OauthProtectedResource(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	baseURL := h.getBaseURL(r)
	u, _ := url.Parse(baseURL)
	mcpServerURL := u.JoinPath("/mcp", srv.Name)
	authServerURL := u.JoinPath("/mcp", srv.Name)
	metadata := map[string]any{
		"authorization_servers": []string{
			authServerURL.String(),
		},
		"bearer_methods_supported": []string{"header"},
		"resource":                 mcpServerURL.String(),
		"resource_documentation":   mcpServerURL.String(),
	}
	if srv.OAuth2 != nil && len(srv.OAuth2.Scopes) > 0 {
		metadata["scopes_supported"] = srv.OAuth2.Scopes
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

// MetadataEndpoint serves /.well-known/oauth-authorization-server
func (h *AuthHandler) MetadataEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	baseURL := h.getBaseURL(r)
	metadata := map[string]any{
		"issuer":                                baseURL,
		"authorization_endpoint":                fmt.Sprintf("%s/%s/auth/login", baseURL, srv.Name),
		"token_endpoint":                        fmt.Sprintf("%s/%s/auth/token", baseURL, srv.Name),
		"registration_endpoint":                 fmt.Sprintf("%s/%s/auth/clients", baseURL, srv.Name),
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"resource_indicators_supported":         true,
	}
	if srv.OAuth2 != nil && len(srv.OAuth2.Scopes) > 0 {
		metadata["scopes_supported"] = srv.OAuth2.Scopes
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

// RegisterClientEndpoint POST /auth/clients リクエストを処理します（動的クライアント登録、RFC 7591）。
func (h *AuthHandler) RegisterClientEndpoint(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(w http.ResponseWriter, status int, errCode string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": errCode})
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		ClientName              string   `json:"client_name"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, "invalid_client_metadata")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, "invalid_redirect_uri")
		return
	}

	if len(req.GrantTypes) == 0 {
		req.GrantTypes = []string{"authorization_code"}
	}
	if len(req.ResponseTypes) == 0 {
		req.ResponseTypes = []string{"code"}
	}
	if req.TokenEndpointAuthMethod == "" {
		req.TokenEndpointAuthMethod = "none"
	}

	clientID := generateRandomString(32)
	reg := ClientRegistration{
		ClientID:                clientID,
		ClientIDIssuedAt:        time.Now().Unix(),
		RedirectURIs:            req.RedirectURIs,
		GrantTypes:              req.GrantTypes,
		ResponseTypes:           req.ResponseTypes,
		ClientName:              req.ClientName,
		TokenEndpointAuthMethod: req.TokenEndpointAuthMethod,
	}
	regJSON, _ := json.Marshal(reg)
	if err := h.redisClient.Set(r.Context(), "oauth_client:"+clientID, regJSON, 90*24*time.Hour); err != nil {
		slog.Error("failed to store client registration", slog.Any("error", err))
		writeJSON(w, http.StatusInternalServerError, "server_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(reg)
}

func (h *AuthHandler) LoginEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	if srv == nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}
	// OAuth2 設定がない HTTP MCP バックエンドは OAuth2.1 Auto-Discovery を試みる
	if srv.OAuth2 == nil {
		if !srv.IsMCPBackend() || srv.Transport != config.MCPTransportHTTP {
			http.Error(w, "oauth2 not configured for this server", http.StatusInternalServerError)
			return
		}
		discovered, err := h.discoverOAuth2(r.Context(), srv, h.getBaseURL(r))
		if err != nil {
			slog.ErrorContext(r.Context(), "oauth2 discovery failed",
				slog.String("server", srv.Name), slog.String("error", err.Error()))
			http.Error(w, "oauth2 discovery failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		srv.OAuth2 = discovered
	}

	q := r.URL.Query()
	codeChallenge := q.Get("code_challenge")
	codeChallengeMethod := q.Get("code_challenge_method")
	clientID := q.Get("client_id")
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	resource := q.Get("resource") // RFC 8707

	slog.Info("LoginEndpoint called", //nolint: gosec
		slog.String("client_id", util.SanitizeLog(clientID)),
		slog.String("redirect_uri", util.SanitizeLog(redirectURI)),
		slog.String("state", util.SanitizeLog(state)),
		slog.String("code_challenge", util.SanitizeLog(codeChallenge)),
	)

	if codeChallenge == "" || codeChallengeMethod != "S256" {
		slog.Warn("invalid login request", slog.String("reason", "missing_pkce"))
		http.Error(w, "invalid_request: code_challenge or code_challenge_method missing/invalid", http.StatusBadRequest)
		return
	}

	sessionID := generateRandomString(32)
	// 上流認可サーバー向けの PKCE verifier を生成（RFC 7636）
	upstreamVerifier := generateRandomString(43)

	session := AuthSession{
		ClientID:             clientID,
		RedirectURI:          redirectURI,
		State:                state,
		CodeChallenge:        codeChallenge,
		CodeChallengeMethod:  codeChallengeMethod,
		Resource:             resource,
		OAuth2ClientID:       srv.OAuth2.ClientID,
		OAuth2ClientSecret:   srv.OAuth2.ClientSecret,
		OAuth2TokenURL:       srv.OAuth2.TokenURL,
		OAuth2Scopes:         srv.OAuth2.Scopes,
		UpstreamCodeVerifier: upstreamVerifier,
	}

	baseURL := h.getBaseURL(r)
	callbackURL := fmt.Sprintf("%s/%s/auth/callback", baseURL, srv.Name)

	sessionJSON, _ := json.Marshal(session)
	if err := h.redisClient.Set(r.Context(), "auth_session:"+sessionID, sessionJSON, 10*time.Minute); err != nil {
		slog.Error("failed to store auth session", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	oauthCfg := &oauth2.Config{
		ClientID:     srv.OAuth2.ClientID,
		ClientSecret: srv.OAuth2.ClientSecret,
		RedirectURL:  callbackURL,
		Scopes:       srv.OAuth2.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  srv.OAuth2.AuthURL,
			TokenURL: srv.OAuth2.TokenURL,
		},
	}
	redirectURL := oauthCfg.AuthCodeURL(sessionID, oauth2.S256ChallengeOption(upstreamVerifier))
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *AuthHandler) CallbackEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	q := r.URL.Query()
	sessionID := q.Get("state")
	code := q.Get("code")

	slog.Info("CallbackEndpoint called", //nolint: gosec
		slog.String("state", util.SanitizeLog(sessionID)),
		slog.Bool("has_code", code != ""),
	)

	if sessionID == "" || code == "" {
		slog.Warn("callback missing params")
		http.Error(w, "missing state or code", http.StatusBadRequest)
		return
	}

	sessionJSON, err := h.redisClient.Get(r.Context(), "auth_session:"+sessionID)
	if err != nil {
		slog.Warn("session not found in redis", slog.String("session_id", util.SanitizeLog(sessionID))) //nolint: gosec
		http.Error(w, "invalid or expired session", http.StatusBadRequest)
		return
	}
	var session AuthSession
	_ = json.Unmarshal([]byte(sessionJSON), &session)
	_ = h.redisClient.Del(r.Context(), "auth_session:"+sessionID)

	baseURL := h.getBaseURL(r)
	callbackURL := fmt.Sprintf("%s/%s/auth/callback", baseURL, srv.Name)

	// Exchange code with upstream OAuth2 server
	upstreamToken, err := h.exchangeUpstreamToken(r.Context(), session, code, callbackURL)
	if err != nil {
		slog.Error("failed to exchange code with upstream", slog.Any("error", err))
		http.Error(w, "failed to authenticate with upstream", http.StatusInternalServerError)
		return
	}

	tokenTTL := time.Hour
	if !upstreamToken.Expiry.IsZero() {
		if ttl := time.Until(upstreamToken.Expiry); ttl > 0 {
			tokenTTL = ttl
		}
	}

	tokenKey := "upstream_token:" + generateRandomString(32)
	tokenJSON, _ := json.Marshal(upstreamToken)
	if err := h.redisClient.Set(r.Context(), tokenKey, tokenJSON, tokenTTL); err != nil {
		slog.Error("failed to store upstream token", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	mcpCode := generateRandomString(32)
	authCodeData := AuthCodeData{
		CodeChallenge:       session.CodeChallenge,
		CodeChallengeMethod: session.CodeChallengeMethod,
		Resource:            session.Resource,
		UpstreamTokenKey:    tokenKey,
	}
	authCodeJSON, _ := json.Marshal(authCodeData)
	if err := h.redisClient.Set(r.Context(), "auth_code:"+mcpCode, authCodeJSON, 5*time.Minute); err != nil {
		slog.Error("failed to store auth code", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	redirectURI := fmt.Sprintf("%s?code=%s&state=%s", session.RedirectURI, mcpCode, session.State)
	http.Redirect(w, r, redirectURI, http.StatusFound)
}

func (h *AuthHandler) TokenEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	grantType := r.FormValue("grant_type")
	code := r.FormValue("code")
	codeVerifier := r.FormValue("code_verifier")
	resource := r.FormValue("resource") // RFC 8707

	slog.Info("TokenEndpoint called", //nolint: gosec
		slog.String("grant_type", util.SanitizeLog(grantType)),
		slog.Bool("has_code", code != ""),
		slog.Bool("has_verifier", codeVerifier != ""),
	)

	if grantType != "authorization_code" {
		slog.Warn("unsupported grant type", slog.String("grant_type", util.SanitizeLog(grantType))) //nolint: gosec
		http.Error(w, "unsupported_grant_type", http.StatusBadRequest)
		return
	}

	if code == "" || codeVerifier == "" {
		slog.Warn("missing code or verifier")
		http.Error(w, "missing code or code_verifier", http.StatusBadRequest)
		return
	}

	authCodeJSON, err := h.redisClient.Get(r.Context(), "auth_code:"+code)
	if err != nil {
		slog.Warn("auth code not found in redis", slog.String("code", util.SanitizeLog(code))) //nolint: gosec
		http.Error(w, "invalid or expired code", http.StatusBadRequest)
		return
	}
	var authCodeData AuthCodeData
	_ = json.Unmarshal([]byte(authCodeJSON), &authCodeData)

	// Verify PKCE S256
	expectedChallenge := generateS256Challenge(codeVerifier)
	if expectedChallenge != authCodeData.CodeChallenge {
		http.Error(w, "invalid code_verifier", http.StatusBadRequest)
		return
	}

	// Validate resource parameter (RFC 8707): if both sides specify resource, they must match
	if resource != "" && authCodeData.Resource != "" && resource != authCodeData.Resource {
		http.Error(w, "invalid_target: resource parameter mismatch", http.StatusBadRequest)
		return
	}

	_ = h.redisClient.Del(r.Context(), "auth_code:"+code)

	tokenJSON, err := h.redisClient.Get(r.Context(), authCodeData.UpstreamTokenKey)
	if err != nil {
		slog.Error("upstream token not found in redis", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var upstreamToken oauth2.Token
	_ = json.Unmarshal([]byte(tokenJSON), &upstreamToken)

	var expiresIn int64
	if !upstreamToken.Expiry.IsZero() {
		if secs := int64(time.Until(upstreamToken.Expiry).Seconds()); secs > 0 {
			expiresIn = secs
		}
	}

	resp := map[string]any{
		"access_token": upstreamToken.AccessToken,
		"token_type":   upstreamToken.TokenType,
		"expires_in":   expiresIn,
	}
	if upstreamToken.RefreshToken != "" {
		resp["refresh_token"] = upstreamToken.RefreshToken
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *AuthHandler) exchangeUpstreamToken(ctx context.Context, session AuthSession, code, callbackURL string) (*oauth2.Token, error) {
	oauthCfg := &oauth2.Config{
		ClientID:     session.OAuth2ClientID,
		ClientSecret: session.OAuth2ClientSecret,
		RedirectURL:  callbackURL,
		Scopes:       session.OAuth2Scopes,
		Endpoint: oauth2.Endpoint{
			TokenURL: session.OAuth2TokenURL,
		},
	}
	var opts []oauth2.AuthCodeOption
	if session.UpstreamCodeVerifier != "" {
		opts = append(opts, oauth2.VerifierOption(session.UpstreamCodeVerifier))
	}
	token, err := oauthCfg.Exchange(ctx, code, opts...)
	if err != nil {
		return nil, fmt.Errorf("upstream token exchange failed: %w", err)
	}
	return token, nil
}

// discoverOAuth2 は HTTP MCP バックエンドに対して OAuth2 エンドポイントを自動発見し、
// Dynamic Client Registration で ClientID を取得して config.OAuth2 を返す。
// 結果は h.servers にキャッシュされる。
func (h *AuthHandler) discoverOAuth2(ctx context.Context, srv *config.Server, gatewayBaseURL string) (*config.OAuth2, error) {
	// キャッシュを確認
	h.mu.RLock()
	if cached, ok := h.servers[srv.Name]; ok && cached.OAuth2 != nil {
		h.mu.RUnlock()
		return cached.OAuth2, nil
	}
	h.mu.RUnlock()

	// Step 1: バックエンドに probe リクエストを送り 401 を取得
	probeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL,
		strings.NewReader(`{"jsonrpc":"2.0","method":"initialize","id":1,"params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"probe","version":"0"}}}`))
	if err != nil {
		return nil, fmt.Errorf("probe request build failed: %w", err)
	}
	probeReq.Header.Set("Content-Type", "application/json")
	probeReq.Header.Set("Accept", "application/json, text/event-stream")

	probeResp, err := http.DefaultClient.Do(probeReq)
	if err != nil {
		return nil, fmt.Errorf("probe request failed: %w", err)
	}
	defer probeResp.Body.Close()

	if probeResp.StatusCode != http.StatusUnauthorized {
		return nil, fmt.Errorf("backend did not return 401 (got %d); cannot discover OAuth2", probeResp.StatusCode)
	}

	// Step 2: WWW-Authenticate を解析して resource_metadata URL を取得
	challenges, err := oauthex.ParseWWWAuthenticate(probeResp.Header["Www-Authenticate"])
	if err != nil || len(challenges) == 0 {
		return nil, fmt.Errorf("could not parse WWW-Authenticate header from backend")
	}
	var resourceMetaURL string
	for _, c := range challenges {
		if u, ok := c.Params["resource_metadata"]; ok {
			resourceMetaURL = u
			break
		}
	}
	if resourceMetaURL == "" {
		return nil, fmt.Errorf("no resource_metadata found in WWW-Authenticate")
	}

	// Step 3: Protected Resource Metadata を取得して認可サーバーを特定
	prm, err := oauthex.GetProtectedResourceMetadata(ctx, resourceMetaURL, srv.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("get protected resource metadata: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization_servers in protected resource metadata")
	}

	// Step 4: 認可サーバーのメタデータを取得
	authMeta, err := mcpauth.GetAuthServerMetadata(ctx, prm.AuthorizationServers[0], nil)
	if err != nil {
		return nil, fmt.Errorf("get auth server metadata: %w", err)
	}
	if authMeta == nil {
		return nil, fmt.Errorf("no auth server metadata found at %s", prm.AuthorizationServers[0])
	}

	// Step 5: Dynamic Client Registration で ClientID を取得
	clientID := ""
	if authMeta.RegistrationEndpoint != "" {
		callbackURL := fmt.Sprintf("%s/%s/auth/callback", gatewayBaseURL, srv.Name)
		regResp, err := oauthex.RegisterClient(ctx, authMeta.RegistrationEndpoint,
			&oauthex.ClientRegistrationMetadata{
				RedirectURIs: []string{callbackURL},
				ClientName:   "manifold",
				GrantTypes:   []string{"authorization_code"},
			}, nil)
		if err != nil {
			return nil, fmt.Errorf("dynamic client registration: %w", err)
		}
		clientID = regResp.ClientID
	}

	oauth2cfg := &config.OAuth2{
		ClientID: clientID,
		AuthURL:  authMeta.AuthorizationEndpoint,
		TokenURL: authMeta.TokenEndpoint,
		Scopes:   authMeta.ScopesSupported,
	}

	// キャッシュに保存
	h.mu.Lock()
	if s, ok := h.servers[srv.Name]; ok {
		s.OAuth2 = oauth2cfg
		h.servers[srv.Name] = s
	}
	h.mu.Unlock()

	slog.InfoContext(ctx, "oauth2 discovered for mcp backend",
		slog.String("server", srv.Name),
		slog.String("auth_url", oauth2cfg.AuthURL),
		slog.String("token_url", oauth2cfg.TokenURL),
		slog.String("client_id", oauth2cfg.ClientID),
	)
	return oauth2cfg, nil
}

func (h *AuthHandler) getBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// リバプロがいる場合
	if forwardedProto := r.Header.Get("X-Forwarded-Proto"); forwardedProto != "" {
		scheme = forwardedProto
	}
	return fmt.Sprintf("%s://%s", scheme, r.Host)
}

func generateRandomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)[:n]
}

func generateS256Challenge(codeVerifier string) string {
	hash := sha256.Sum256([]byte(codeVerifier))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}
