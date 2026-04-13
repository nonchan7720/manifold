package httphandler

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
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
	MCPServerName        string `json:"mcp_server_name"`
}

// AuthCodeData 認証コードとトークンの交換に関するデータを保持。
type AuthCodeData struct {
	ClientID            string `json:"client_id,omitempty"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource,omitempty"`
	UpstreamTokenKey    string `json:"upstream_token_key"` // アップストリーム・トークンのRedisキー
	MCPServerName       string `json:"mcp_server_name"`
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

type StoreClientRegistration struct {
	ClientRegistration
	MCPServerName string `json:"mcp_server_name"`
}

// AuthHandler CLIおよびMCPクライアントの両方に対して、OAuth 2.1認証サーバーを実装。
type AuthHandler struct {
	store       store.Client
	servers     config.Servers
	mu          sync.RWMutex
	tokenEncKey []byte
	httpClient  *http.Client
}

type AuthHandlerOption func(h *AuthHandler)

func WithEncryptKey(key []byte) AuthHandlerOption {
	return func(h *AuthHandler) {
		h.tokenEncKey = slices.Clone(key)
	}
}

func WithEncryptKeyByBase64(key string) AuthHandlerOption {
	v, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		panic(err)
	}
	return WithEncryptKey(v)
}

func NewAuthHandler(storeClient store.Client, servers config.Servers, opts ...AuthHandlerOption) *AuthHandler {
	h := &AuthHandler{
		store:      storeClient,
		servers:    servers,
		httpClient: newSafeHTTPClient(),
	}
	for _, opt := range opts {
		opt(h)
	}
	if len(h.tokenEncKey) == 0 {
		h.tokenEncKey = make([]byte, 32)
		if _, err := rand.Read(h.tokenEncKey); err != nil {
			panic(fmt.Sprintf("failed to generate token encryption key: %v", err))
		}
	}
	return h
}

func (h *AuthHandler) RegisterRoutes(mux *http.ServeMux, pathServerName string, middleware func(h http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc(fmt.Sprintf("GET /.well-known/oauth-protected-resource/mcp/{%s}", pathServerName), middleware(wrapMCPServer(h.OauthProtectedResource)))
	mux.HandleFunc(fmt.Sprintf("GET /.well-known/oauth-authorization-server/mcp/{%s}", pathServerName), middleware(wrapMCPServer(h.MetadataEndpoint)))
	mux.HandleFunc(fmt.Sprintf("GET /{%s}/auth/login", pathServerName), middleware(wrapMCPServer(h.LoginEndpoint)))
	mux.HandleFunc("GET /authorize", wrapMCPServer(h.LoginEndpoint))
	mux.HandleFunc(fmt.Sprintf("GET /{%s}/auth/callback", pathServerName), middleware(wrapMCPServer(h.CallbackEndpoint)))
	mux.HandleFunc("GET /callback", wrapMCPServer(h.CallbackEndpoint))
	mux.HandleFunc(fmt.Sprintf("POST /{%s}/auth/token", pathServerName), middleware(wrapMCPServer(h.TokenEndpoint)))
	mux.HandleFunc("POST /token", wrapMCPServer(h.TokenEndpoint))
	// // Dynamic Client Registration (RFC 7591)
	mux.HandleFunc(fmt.Sprintf("POST /{%s}/auth/clients", pathServerName), middleware(wrapMCPServer(h.RegisterClientEndpoint)))
	mux.HandleFunc("POST /register", h.RegisterClientEndpointByClaudeCode)
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
func (h *AuthHandler) RegisterClientEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) {
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

	// redirect_uri スキームを検証（https または http://localhost のみ許可）
	for _, uri := range req.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			slog.Warn("invalid redirect_uri in client registration", slog.String("uri", util.SanitizeLog(uri)))
			writeJSON(w, http.StatusBadRequest, "invalid_redirect_uri")
			return
		}
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
	storeReg := StoreClientRegistration{
		ClientRegistration: reg,
		MCPServerName:      srv.Name,
	}
	regJSON, _ := json.Marshal(storeReg)
	if err := h.store.Set(r.Context(), "oauth_client:"+clientID, regJSON, 90*24*time.Hour); err != nil {
		slog.Error("failed to store client registration", slog.Any("error", err))
		writeJSON(w, http.StatusInternalServerError, "server_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(reg)
}

var (
	claudeCodeClientName = regexp.MustCompile(`Claude Code \(([^)]+)\)`)
)

// RegisterClientEndpoint POST /auth/clients リクエストを処理します（動的クライアント登録、RFC 7591）。
func (h *AuthHandler) RegisterClientEndpointByClaudeCode(w http.ResponseWriter, r *http.Request) {
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

	match := claudeCodeClientName.FindStringSubmatch(req.ClientName)

	var mcpName string
	if len(match) > 1 {
		mcpName = match[1]
	}
	if mcpName == "" {
		writeJSON(w, http.StatusBadRequest, "invalid_client_name")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, "invalid_redirect_uri")
		return
	}

	// redirect_uri スキームを検証（https または http://localhost のみ許可）
	for _, uri := range req.RedirectURIs {
		if err := validateRedirectURI(uri); err != nil {
			slog.Warn("invalid redirect_uri in client registration", slog.String("uri", util.SanitizeLog(uri)))
			writeJSON(w, http.StatusBadRequest, "invalid_redirect_uri")
			return
		}
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
	storeReg := StoreClientRegistration{
		ClientRegistration: reg,
		MCPServerName:      mcpName,
	}
	regJSON, _ := json.Marshal(storeReg)
	if err := h.store.Set(r.Context(), "oauth_client:"+clientID, regJSON, 90*24*time.Hour); err != nil {
		slog.Error("failed to store client registration", slog.Any("error", err))
		writeJSON(w, http.StatusInternalServerError, "server_error")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(reg)
}

func (h *AuthHandler) LoginEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) {
	ctx := r.Context()
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
		slog.WarnContext(ctx, "invalid login request", slog.String("reason", "missing_pkce"))
		http.Error(w, "invalid_request: code_challenge or code_challenge_method missing/invalid", http.StatusBadRequest)
		return
	}

	// client_id が提供された場合、登録済みの redirect_uri と照合してオープンリダイレクトを防ぐ
	var clientReg StoreClientRegistration
	if clientID != "" {
		clientJSON, err := h.store.Get(r.Context(), "oauth_client:"+clientID)
		if err != nil {
			slog.WarnContext(ctx, "unknown client_id in login request", slog.String("client_id", util.SanitizeLog(clientID)))
			http.Error(w, "invalid_client", http.StatusUnauthorized)
			return
		}
		if err := json.Unmarshal([]byte(clientJSON), &clientReg); err != nil {
			slog.ErrorContext(ctx, "failed to unmarshal client registration", slog.Any("error", err))
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		if !containsString(clientReg.RedirectURIs, redirectURI) {
			slog.WarnContext(ctx, "redirect_uri not registered for client",
				slog.String("client_id", util.SanitizeLog(clientID)),
				slog.String("redirect_uri", util.SanitizeLog(redirectURI)))
			http.Error(w, "invalid_redirect_uri", http.StatusBadRequest)
			return
		}
	} else {
		slog.ErrorContext(ctx, "failed to client_id is empty")
		http.Error(w, "invalid_client_id", http.StatusBadRequest)
		return
	}

	if srv == nil {
		if v, ok := h.servers[clientReg.MCPServerName]; ok {
			srv = v
		}
	}
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
		discovered, err := h.discoverOAuth2(ctx, srv, h.getBaseURL(r))
		if err != nil {
			slog.ErrorContext(ctx, "oauth2 discovery failed",
				slog.String("server", srv.Name), slog.String("error", err.Error()))
			http.Error(w, "oauth2 discovery failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		srv.OAuth2 = discovered
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
	if err := h.store.Set(r.Context(), "auth_session:"+sessionID, sessionJSON, 10*time.Minute); err != nil {
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

	sessionJSON, err := h.store.Get(r.Context(), "auth_session:"+sessionID)
	if err != nil {
		slog.Warn("session not found in redis", slog.String("session_id", util.SanitizeLog(sessionID))) //nolint: gosec
		http.Error(w, "invalid or expired session", http.StatusBadRequest)
		return
	}
	var session AuthSession
	if err := json.Unmarshal([]byte(sessionJSON), &session); err != nil {
		slog.Error("failed to unmarshal auth session", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	_ = h.store.Del(r.Context(), "auth_session:"+sessionID)
	if srv == nil {
		if v, ok := h.servers[session.MCPServerName]; ok {
			srv = v
		}
	}
	if srv == nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

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
	tokenJSON, err := json.Marshal(upstreamToken) //nolint: gosec
	if err != nil {
		slog.Error("failed to marshal upstream token", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// AES-256-GCM でトークンを暗号化して保存（平文保存を防ぐ）
	encryptedToken, err := h.encryptToken(tokenJSON)
	if err != nil {
		slog.Error("failed to encrypt upstream token", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if err := h.store.Set(r.Context(), tokenKey, encryptedToken, tokenTTL); err != nil {
		slog.Error("failed to store upstream token", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	mcpCode := generateRandomString(32)
	authCodeData := AuthCodeData{
		ClientID:            session.ClientID,
		CodeChallenge:       session.CodeChallenge,
		CodeChallengeMethod: session.CodeChallengeMethod,
		Resource:            session.Resource,
		UpstreamTokenKey:    tokenKey,
	}
	authCodeJSON, _ := json.Marshal(authCodeData)
	if err := h.store.Set(r.Context(), "auth_code:"+mcpCode, authCodeJSON, 5*time.Minute); err != nil {
		slog.Error("failed to store auth code", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	redirectURI := fmt.Sprintf("%s?code=%s&state=%s", session.RedirectURI, mcpCode, session.State)
	http.Redirect(w, r, redirectURI, http.StatusFound)
}

func (h *AuthHandler) TokenEndpoint(w http.ResponseWriter, r *http.Request, srv *config.Server) { //nolint: gocyclo
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	grantType := r.FormValue("grant_type")
	code := r.FormValue("code")
	codeVerifier := r.FormValue("code_verifier")
	clientID := r.FormValue("client_id")
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

	authCodeJSON, err := h.store.Get(r.Context(), "auth_code:"+code)
	if err != nil {
		slog.Warn("auth code not found in redis", slog.String("code", util.SanitizeLog(code))) //nolint: gosec
		http.Error(w, "invalid or expired code", http.StatusBadRequest)
		return
	}
	var authCodeData AuthCodeData
	if err := json.Unmarshal([]byte(authCodeJSON), &authCodeData); err != nil {
		slog.Error("failed to unmarshal auth code data", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	// client_id をコード発行時のクライアントと照合
	if authCodeData.ClientID != "" && clientID != authCodeData.ClientID {
		slog.Warn("client_id mismatch in token request", //nolint: gosec
			slog.String("expected", authCodeData.ClientID),
			slog.String("got", util.SanitizeLog(clientID)))
		http.Error(w, "invalid_client", http.StatusUnauthorized)
		return
	}

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

	_ = h.store.Del(r.Context(), "auth_code:"+code)

	encryptedToken, err := h.store.Get(r.Context(), authCodeData.UpstreamTokenKey)
	if err != nil {
		slog.Error("upstream token not found in redis", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	tokenJSON, err := h.decryptToken(encryptedToken)
	if err != nil {
		slog.Error("failed to decrypt upstream token", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	var upstreamToken oauth2.Token
	if err := json.Unmarshal(tokenJSON, &upstreamToken); err != nil {
		slog.Error("failed to unmarshal upstream token", slog.Any("error", err))
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

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
	ctx = context.WithValue(ctx, oauth2.HTTPClient, h.httpClient)
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

var (
	//go:embed initialize.json
	initializeJSON string
)

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
	wwwAuthenticate, err := sendProbeRequest(ctx, srv.URL)
	if err != nil {
		return nil, err
	}
	// Step 2: WWW-Authenticate を解析して resource_metadata URL を取得
	// resource_metadata がない場合は RFC 9728 に従い well-known URL にフォールバック
	resourceMetaURL, err := getResourceMetadata(wwwAuthenticate)
	if err != nil {
		u, parseErr := url.Parse(srv.URL)
		if parseErr != nil {
			return nil, err
		}
		resourceMetaURL = fmt.Sprintf("%s://%s/.well-known/oauth-protected-resource", u.Scheme, u.Host)
	}

	// Step 3: Protected Resource Metadata を取得して認可サーバーを特定
	// resource フィールドの検証に使うURLは resourceMetaURL のオリジン（スキーム+ホスト）にする。
	// srv.URL にはパス（/mcp 等）が含まれる場合があり、メタデータが返す resource と一致しないことがある。
	resourceOrigin := srv.URL
	if u, err := url.Parse(resourceMetaURL); err == nil {
		resourceOrigin = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	}
	authorizationServers, err := getAuthorizationServers(ctx, resourceMetaURL, resourceOrigin, h.httpClient)
	if err != nil {
		return nil, err
	}

	// Step 4: 認可サーバーのメタデータを取得
	var (
		authMeta *oauthex.AuthServerMeta
		lastErr  error
	)
	for _, server := range authorizationServers {
		var err error
		authMeta, err = getAuthMetadata(ctx, server, h.httpClient)
		if err == nil {
			break
		} else {
			lastErr = err
			continue
		}
	}

	if lastErr != nil {
		return nil, err
	}

	// Step 5: Dynamic Client Registration で ClientID/ClientSecret を取得
	clientID := ""
	clientSecret := ""
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
		clientSecret = regResp.ClientSecret
	}

	oauth2cfg := &config.OAuth2{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		AuthURL:      authMeta.AuthorizationEndpoint,
		TokenURL:     authMeta.TokenEndpoint,
		Scopes:       authMeta.ScopesSupported,
	}

	// キャッシュに保存
	h.mu.Lock()
	if s, ok := h.servers[srv.Name]; ok {
		s.OAuth2 = oauth2cfg
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

func sendProbeRequest(ctx context.Context, url string) ([]string, error) {
	probeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(initializeJSON))
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
	return probeResp.Header["Www-Authenticate"], nil
}

func getResourceMetadata(wwwAuthenticate []string) (string, error) {
	challenges, err := oauthex.ParseWWWAuthenticate(wwwAuthenticate)
	if err != nil || len(challenges) == 0 {
		return "", fmt.Errorf("could not parse WWW-Authenticate header from backend")
	}
	var resourceMetaURL string
	for _, c := range challenges {
		if u, ok := c.Params["resource_metadata"]; ok {
			resourceMetaURL = u
			break
		}
	}
	if resourceMetaURL == "" {
		return "", fmt.Errorf("no resource_metadata found in WWW-Authenticate")
	}
	return resourceMetaURL, nil
}

func getAuthorizationServers(ctx context.Context, resourceMetaURL, url string, c *http.Client) ([]string, error) {
	prm, err := oauthex.GetProtectedResourceMetadata(ctx, resourceMetaURL, url, c)
	if err != nil {
		return nil, fmt.Errorf("get protected resource metadata: %w", err)
	}
	if len(prm.AuthorizationServers) == 0 {
		return nil, fmt.Errorf("no authorization_servers in protected resource metadata")
	}
	return prm.AuthorizationServers, nil
}

func getAuthMetadata(ctx context.Context, authorizationServer string, c *http.Client) (*oauthex.AuthServerMeta, error) {
	authMeta, err := mcpauth.GetAuthServerMetadata(ctx, authorizationServer, c)
	if err != nil {
		return nil, fmt.Errorf("get auth server metadata: %w", err)
	}
	if authMeta == nil {
		return nil, fmt.Errorf("no auth server metadata found at %s", authorizationServer)
	}
	return authMeta, nil
}

// encryptToken は平文バイト列を AES-256-GCM で暗号化し、base64 エンコードした文字列を返す。
func (h *AuthHandler) encryptToken(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(h.tokenEncKey)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	// nonce をプレフィックスとして暗号文に結合
	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// decryptToken は encryptToken で暗号化された base64 文字列を復号する。
func (h *AuthHandler) decryptToken(encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	block, err := aes.NewCipher(h.tokenEncKey)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

// validateRedirectURI は redirect_uri が許可されたスキームを使用しているか検証する。
// https:// と http://localhost (127.0.0.1, ::1) のみ許可。
func validateRedirectURI(rawURI string) error {
	u, err := url.Parse(rawURI)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri: %w", err)
	}
	if u.Fragment != "" {
		return fmt.Errorf("redirect_uri must not contain a fragment")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil
		}
	}
	return fmt.Errorf("redirect_uri must use https or http://localhost")
}

func newSafeHTTPClient() *http.Client {
	dialer := &net.Dialer{
		ControlContext: func(ctx context.Context, network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("invalid address %q: %w", address, err)
			}
			if ip := net.ParseIP(host); ip != nil {
				// IP リテラル（通常はここに来る）
				if isPrivateIP(ip) {
					return fmt.Errorf("connection to private IP %s is not allowed", ip)
				}
			} else {
				// ホスト名が渡された場合（エッジケース）のフォールバック
				addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return fmt.Errorf("DNS resolution failed for %q: %w", host, err)
				}
				for _, ipAddr := range addrs {
					if isPrivateIP(ipAddr.IP) {
						return fmt.Errorf("connection to private IP %s is not allowed", ipAddr.IP)
					}
				}
			}
			return nil
		},
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: 30 * time.Second,
	}
}

// isPrivateIP は IP アドレスがプライベート・リンクローカル・ループバック等の
// 予約済みレンジに属するか確認する。
func isPrivateIP(ip net.IP) bool {
	for _, cidr := range privateIPRanges {
		if cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// privateIPRanges はプライベート・予約済み IP レンジのリスト。
var privateIPRanges []*net.IPNet

func init() {
	privateCIDRs := []string{
		"127.0.0.0/8",    // IPv4 ループバック
		"::1/128",        // IPv6 ループバック
		"10.0.0.0/8",     // プライベート
		"172.16.0.0/12",  // プライベート
		"192.168.0.0/16", // プライベート
		"169.254.0.0/16", // IPv4 リンクローカル
		"fe80::/10",      // IPv6 リンクローカル
		"fc00::/7",       // IPv6 ユニークローカル
		"100.64.0.0/10",  // 共有アドレス空間 (RFC 6598)
		"0.0.0.0/8",      // 未指定
	}
	for _, cidr := range privateCIDRs {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			privateIPRanges = append(privateIPRanges, network)
		}
	}
}

// containsString は slice の中に target が含まれるか確認する。
func containsString(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
