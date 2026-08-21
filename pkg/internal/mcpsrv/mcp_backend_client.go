package mcpsrv

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/version"
)

// MCPBackendClient はバックエンドの MCP サーバーへの接続を管理する。
// 遅延接続方式を採用し、最初のリクエスト時（認証トークン入りコンテキスト）に接続する。
type MCPBackendClient struct {
	name string
	cfg  *config.Server
	srv  *mcp.Server // ゲートウェイ側の MCP サーバー（ツール登録先）

	mu        sync.Mutex
	session   *mcp.ClientSession
	connected bool
}

// EnsureConnected は初回のみバックエンドへ接続してツールを登録する。
// 接続失敗時は次のリクエストでリトライ可能（sync.Once は使わない）。
func (c *MCPBackendClient) EnsureConnected(ctx context.Context) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/EnsureConnected")
	defer func() { trace.EndSpan(ctx, rErr) }()

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

func (c *MCPBackendClient) connect(ctx context.Context) (_ *mcp.ClientSession, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/connect")
	defer func() { trace.EndSpan(ctx, rErr) }()

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "manifold",
		Version: version.Version,
	}, nil)

	transport, err := c.buildTransport(ctx)
	if err != nil {
		return nil, err
	}
	return client.Connect(ctx, transport, nil)
}

func (c *MCPBackendClient) buildTransport(ctx context.Context) (_ mcp.Transport, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/buildTransport")
	defer func() { trace.EndSpan(ctx, rErr) }()

	switch c.cfg.Transport {
	case config.MCPTransportHTTP:
		return &mcp.StreamableClientTransport{
			Endpoint: c.cfg.URL,
			HTTPClient: &http.Client{
				Transport: httpClientRoundTripper(
					c.cfg.AuthValue,
					c.cfg.OAuth2,
					c.cfg.TokenExchange,
					c.cfg.ExtraHeaders,
				),
			},
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

	case config.MCPTransportReverse:
		return nil, fmt.Errorf(
			"backend %s: reverse transport is not connected via MCPBackendClient",
			c.name,
		)

	default:
		return nil, fmt.Errorf("backend %s: unknown transport %q", c.name, c.cfg.Transport)
	}
}

func (c *MCPBackendClient) registerTools(
	ctx context.Context,
	session *mcp.ClientSession,
) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/registerTools")
	defer func() { trace.EndSpan(ctx, rErr) }()

	result, err := session.ListTools(ctx, nil)
	if err != nil {
		return err
	}
	RegisterSessionTools(c.srv, result.Tools, func(context.Context) (*mcp.ClientSession, error) {
		return session, nil
	})
	return nil
}
