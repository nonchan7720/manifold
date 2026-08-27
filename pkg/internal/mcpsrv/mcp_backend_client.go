package mcpsrv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel/attribute"
)

// MCPBackendClient はバックエンドの MCP サーバーへの接続を管理する。
// 遅延接続方式を採用し、最初のリクエスト時（認証トークン入りコンテキスト）に接続する。
// ツール一覧はゲートウェイ側に登録・固定せず、tools/list・tools/call とも
// このクライアント経由でバックエンドへ毎回転送する（newBackendPassthroughMiddleware）。
type MCPBackendClient struct {
	name string
	cfg  *config.Server

	mu         sync.Mutex
	session    *mcp.ClientSession
	connected  bool
	closed     bool
	connecting *connectAttempt // 進行中の接続試行。なければ nil
}

// connectAttempt は進行中の接続試行を待機者と共有するためのもの。
// err は done がクローズされた後にのみ読んでよい。
type connectAttempt struct {
	done chan struct{}
	err  error
}

// EnsureConnected は初回のみバックエンドへ接続する。
// 同時呼び出しは最初の1つ（リーダー）だけが接続を試行し、他は結果を待つ。
// 待機は ctx を尊重するため、バックエンド障害時でも呼び出し元の期限を超えて
// ブロックしない。接続失敗時は次のリクエストでリトライ可能（sync.Once は使わない）。
func (c *MCPBackendClient) EnsureConnected(ctx context.Context) (rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/EnsureConnected")
	defer func() { trace.EndSpan(ctx, rErr) }()

	for {
		c.mu.Lock()
		switch {
		case c.connected:
			c.mu.Unlock()
			return nil
		case c.closed:
			c.mu.Unlock()
			return fmt.Errorf("backend %s: client closed", c.name)
		case c.connecting != nil:
			att := c.connecting
			c.mu.Unlock()
			select {
			case <-att.done:
			case <-ctx.Done():
				return fmt.Errorf("backend %s: wait for connect: %w", c.name, ctx.Err())
			}
			if att.err != nil {
				// リーダーの ctx 起因の失敗はこの呼び出しの成否を意味しないので、
				// 自分の ctx で試行し直す。それ以外の失敗は共有して即座に返す
				// （待機者が順番に同じ失敗を繰り返さないため）。
				if errors.Is(att.err, context.Canceled) ||
					errors.Is(att.err, context.DeadlineExceeded) {
					continue
				}
				return att.err
			}
			continue
		default:
			att := &connectAttempt{done: make(chan struct{})}
			c.connecting = att
			c.mu.Unlock()
			return c.leadConnect(ctx, att)
		}
	}
}

// leadConnect はリーダーとして接続試行を実行し、結果を待機者へ公開する。
// ロックを保持せずにネットワーク I/O を行うため、Close や待機者をブロックしない。
func (c *MCPBackendClient) leadConnect(ctx context.Context, att *connectAttempt) error {
	session, err := c.connect(ctx)
	if err != nil {
		err = fmt.Errorf("backend %s: connect: %w", c.name, err)
	}

	var staleSession *mcp.ClientSession
	c.mu.Lock()
	c.connecting = nil
	if err == nil {
		if c.closed {
			// 接続中に Close された場合はこのセッションを採用せず破棄する。
			staleSession = session
			err = fmt.Errorf("backend %s: client closed", c.name)
		} else {
			c.session = session
			c.connected = true
		}
	}
	c.mu.Unlock()

	if staleSession != nil {
		staleSession.Close()
	}
	att.err = err
	close(att.done)
	return err
}

// ensureSession は接続を保証した上で現在のセッションを返す。
func (c *MCPBackendClient) ensureSession(ctx context.Context) (*mcp.ClientSession, error) {
	if err := c.EnsureConnected(ctx); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil, fmt.Errorf("backend %s: client closed", c.name)
	}
	return c.session, nil
}

// ListTools はバックエンドへ tools/list を問い合わせる。結果はゲートウェイ側に
// 固定されないため、バックエンドのツール増減や呼び出しユーザーごとの差異が
// そのまま反映される。バックエンドが SEP-2549 の ttlMs を宣言した場合のみ、
// SDK がその期間だけ結果をキャッシュする。
func (c *MCPBackendClient) ListTools(
	ctx context.Context,
	params *mcp.ListToolsParams,
) (_ *mcp.ListToolsResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/ListTools")
	defer func() { trace.EndSpan(ctx, rErr) }()

	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.ListTools(ctx, params)
}

// CallTool はバックエンドへ tools/call をそのまま転送する。
// 引数の検証はバックエンド側の責務とし、ゲートウェイでは行わない。
func (c *MCPBackendClient) CallTool(
	ctx context.Context,
	name string,
	args json.RawMessage,
) (_ *mcp.CallToolResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/CallTool",
		attribute.String("tool-name", name))
	defer func() { trace.EndSpan(ctx, rErr) }()

	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	return session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
}

// ListToolInfos はバックエンドの tools/list を全ページ問い合わせ、
// (name, description) の一覧を返す。
func (c *MCPBackendClient) ListToolInfos(ctx context.Context) ([]ToolInfo, error) {
	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	infos := []ToolInfo{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("backend %s: list tools: %w", c.name, err)
		}
		infos = append(infos, ToolInfo{Name: tool.Name, Description: tool.Description})
	}
	return infos, nil
}

// Close はバックエンドとの接続を閉じ、以降の接続を禁止する。
// 進行中の接続試行を待たずに即座に返る。試行中だったセッションは
// リーダー側が closed フラグを見て破棄する。
func (c *MCPBackendClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
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
