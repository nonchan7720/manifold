package mcpsrv

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"golang.org/x/oauth2"
)

// MCPBackendClient はバックエンドの MCP サーバーへの接続を管理する。
// 遅延接続方式を採用し、最初のリクエスト時（認証トークン入りコンテキスト）に接続する。
type MCPBackendClient struct {
	name string
	cfg  config.Server
	srv  *mcp.Server // ゲートウェイ側の MCP サーバー（ツール登録先）

	mu        sync.Mutex
	session   *mcp.ClientSession
	connected bool
}

// EnsureConnected は初回のみバックエンドへ接続してツールを登録する。
// 接続失敗時は次のリクエストでリトライ可能（sync.Once は使わない）。
func (c *MCPBackendClient) EnsureConnected(ctx context.Context) error {
	c.mu.Lock()
	if c.connected {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	session, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("backend %s: connect: %w", c.name, err)
	}
	if err := c.registerTools(ctx, session); err != nil {
		session.Close()
		return fmt.Errorf("backend %s: register tools: %w", c.name, err)
	}

	c.mu.Lock()
	c.session = session
	c.connected = true
	c.mu.Unlock()
	return nil
}

// Close はバックエンドとの接続を閉じる。
func (c *MCPBackendClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != nil {
		c.session.Close()
		c.session = nil
		c.connected = false
	}
}

func (c *MCPBackendClient) connect(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "manifold",
		Version: "v1.0.0",
	}, nil)

	transport, err := c.buildTransport(ctx)
	if err != nil {
		return nil, err
	}
	return client.Connect(ctx, transport, nil)
}

func (c *MCPBackendClient) buildTransport(ctx context.Context) (mcp.Transport, error) {
	switch c.cfg.Transport {
	case config.MCPTransportHTTP:
		// AuthValue が設定されている場合は API キー等の静的認証。
		// コンテキストのトークンは転送せず、AuthValue のヘッダーを付加する。
		//
		// AuthValue が未設定の場合はコンテキストのトークンを転送する。
		// OAuth2.0（gateway が exchange したトークン）も
		// OAuth2.1（クライアントのトークンをそのまま）もこの経路を通る。
		var oauthHandler auth.OAuthHandler
		baseTransport := http.DefaultTransport
		if c.cfg.AuthValue != nil {
			baseTransport = &authValueRoundTripper{
				authValue: c.cfg.AuthValue,
				base:      http.DefaultTransport,
			}
		} else {
			oauthHandler = &contextOAuthHandler{}
		}
		return &mcp.StreamableClientTransport{
			Endpoint: c.cfg.URL,
			HTTPClient: &http.Client{
				Transport: &extraHeaderRoundTripper{
					headers: c.cfg.ExtraHeaders,
					base:    baseTransport,
				},
			},
			OAuthHandler: oauthHandler,
		}, nil

	case config.MCPTransportStdio:
		if c.cfg.Command == "" {
			return nil, fmt.Errorf("backend %s: command is required for stdio transport", c.name)
		}
		cmd := exec.CommandContext(ctx, c.cfg.Command, c.cfg.Args...) //nolint: gosec
		cmd.Env = os.Environ()
		for k, v := range c.cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		return &mcp.CommandTransport{Command: cmd}, nil

	default:
		return nil, fmt.Errorf("backend %s: unknown transport %q", c.name, c.cfg.Transport)
	}
}

func (c *MCPBackendClient) registerTools(ctx context.Context, session *mcp.ClientSession) error {
	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	for _, tool := range result.Tools {
		t := tool
		c.srv.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return session.CallTool(ctx, &mcp.CallToolParams{
				Name:      req.Params.Name,
				Arguments: req.Params.Arguments,
			})
		})
	}
	return nil
}

// contextOAuthHandler は auth.OAuthHandler の実装。
// go-sdk の StreamableClientTransport は POST リクエスト毎に caller context を使うため、
// 共有セッション1本でユーザーごとのトークン分離が実現できる。
type contextOAuthHandler struct{}

var _ auth.OAuthHandler = (*contextOAuthHandler)(nil)

func (h *contextOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	token := contexts.FromRequestAuthHeader(ctx)
	if token == "" {
		return nil, nil //nolint: nilnil
	}
	bearerToken := strings.TrimPrefix(token, "Bearer ")
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: bearerToken}), nil
}

func (h *contextOAuthHandler) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	defer resp.Body.Close()
	return fmt.Errorf("authorization failed (HTTP %d): re-authenticate with the gateway", resp.StatusCode)
}

// extraHeaderRoundTripper は ExtraHeaders を各 HTTP リクエストに付加する。
type extraHeaderRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
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

// authValueRoundTripper は AuthValue（API キー等の静的認証）を Authorization ヘッダーに付加する。
type authValueRoundTripper struct {
	authValue *config.AuthValue
	base      http.RoundTripper
}

func (t *authValueRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	value := t.authValue.Value
	if t.authValue.Prefix != "" {
		value = t.authValue.Prefix + " " + value
	}
	req.Header.Set(t.authValue.Header, value)
	return t.base.RoundTrip(req)
}
