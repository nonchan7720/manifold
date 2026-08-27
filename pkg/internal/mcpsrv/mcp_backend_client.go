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
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/version"
	"go.opentelemetry.io/otel/attribute"
)

// MCPBackendClient はバックエンドの MCP サーバーへの接続を管理する。
// 接続方針はトランスポートによって異なる。
//
//   - stdio: 1プロセスを全呼び出し元で共有する構造そのものであり、リクエスト
//     ごとにプロセスを起動することはできない。遅延接続方式を採用し、最初の
//     リクエスト時に1回だけ接続し、以降は EnsureConnected/session フィールド
//     を介して同じセッションを共有する（失効時は invalidateSession で破棄し
//     再接続する）。
//   - http: 呼び出し元（ユーザー・テナント）ごとに認証情報が異なるため、
//     セッションを共有すると認証や状態が混線する。ListTools・CallTool・
//     ListToolInfos は呼び出しのたびに呼び出し元の ctx で新しいセッション
//     （initialize や Mcp-Session-Id を含む）を確立し、処理後は必ずクローズ
//     する（完全ステートレス）。session/connected/connecting の共有状態は
//     http では使わない。
//
// ツール一覧はゲートウェイ側に登録・固定せず、tools/list・tools/call とも
// このクライアント経由でバックエンドへ毎回転送する（newBackendPassthroughMiddleware）。
type MCPBackendClient struct {
	name string
	cfg  *config.Server

	// mu 以下は stdio 用の共有セッション状態。http が参照するのは
	// closed（withStatelessSession の Close 済みチェック）のみ。
	mu         sync.Mutex
	session    *mcp.ClientSession
	connected  bool
	closed     bool
	connecting *connectAttempt // 進行中の接続試行。なければ nil

	// waiting は進行中の接続試行を待っている呼び出し数。
	// テストが待機者の合流を決定的に検知するために参照する。
	waiting atomic.Int32
}

// IsPersistent は、このバックエンドが呼び出し元をまたいで単一セッションを
// 共有する方式（stdio）かどうかを返す。true の場合に限り EnsureConnected で
// 事前に接続しておく意味がある。http バックエンドは操作ごとに新しいセッション
// を張るため、事前接続には意味がない。
func (c *MCPBackendClient) IsPersistent() bool {
	return c.cfg.Transport == config.MCPTransportStdio
}

// connectAttempt は進行中の接続試行を待機者と共有するためのもの。
// err は done がクローズされた後にのみ読んでよい。
type connectAttempt struct {
	done chan struct{}
	err  error
}

// EnsureConnected は stdio バックエンド向けに、初回のみバックエンドへ接続する
// （プロセスを起動する）。同時呼び出しは最初の1つ（リーダー）だけが接続を試行
// し、他は結果を待つ。待機は ctx を尊重するため、バックエンド障害時でも呼び出
// し元の期限を超えてブロックしない。接続失敗時は次のリクエストでリトライ可能
// （sync.Once は使わない）。
// http バックエンドは ListTools・CallTool・ListToolInfos が操作ごとに独立した
// セッションを張るためこのメソッドを経由しない（呼び出し自体は動作するが、
// 共有セッションを1本確立するだけで以降使われない）。
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
			c.waiting.Add(1)
			select {
			case <-att.done:
				c.waiting.Add(-1)
			case <-ctx.Done():
				c.waiting.Add(-1)
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

// ensureSession は stdio 用の共有セッションを保証した上で返す。http では使わない
// （呼び出し側は withStatelessSession を使う）。
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

// isDeadSessionError は再接続が必要なセッション失効を示すエラーかを判定する。
// バックエンドが応答した JSON-RPC エラー（ツールの業務エラー等）や HTTP レベルの
// 拒否（認証失敗等）はセッション自体が生きているため対象にしない。
// stdio の共有セッションにのみ意味があり、http の都度接続パスでは使わない
// （毎回新しいセッションを張るため、失効したセッションを使い回すことがない）。
func isDeadSessionError(err error) bool {
	return errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, mcp.ErrSessionMissing)
}

// invalidateSession は session がまだ現在の（stdio 共有）セッションであれば破棄し、
// 次のリクエストで再接続させる。既に別のセッションへ入れ替わっている場合や
// Close 済みの場合は何もしない。
func (c *MCPBackendClient) invalidateSession(session *mcp.ClientSession) {
	c.mu.Lock()
	if c.closed || c.session != session {
		c.mu.Unlock()
		return
	}
	c.session = nil
	c.connected = false
	c.mu.Unlock()
	session.Close()
}

// withStatelessSession は http バックエンド向けに、呼び出し元の ctx で新しい
// セッションを確立して fn を実行し、終了後は必ずクローズする。initialize を
// 含む全 HTTP リクエストが呼び出し元の ctx で送られるため、認証トークン
// （contexts.FromRequestAuthHeader 等）が呼び出し元本人のものになり、
// ユーザー・テナントをまたいでセッションが共有されることがない。
// Close 済みのクライアントでは接続せずエラーを返す（stdio パスと同じ挙動）。
// チェックと接続の間に Close が割り込む可能性は残るが、セッションは fn の
// 終了時に必ずクローズされるため、残留リソースは発生しない。
func (c *MCPBackendClient) withStatelessSession(
	ctx context.Context,
	fn func(*mcp.ClientSession) error,
) error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fmt.Errorf("backend %s: client closed", c.name)
	}

	session, err := c.connect(ctx)
	if err != nil {
		return fmt.Errorf("backend %s: connect: %w", c.name, err)
	}
	defer session.Close()
	return fn(session)
}

// ListTools はバックエンドへ tools/list を問い合わせる。結果はゲートウェイ側に
// 固定されないため、バックエンドのツール増減や呼び出しユーザーごとの差異が
// そのまま反映される。バックエンドが SEP-2549 の ttlMs を宣言した場合のみ、
// SDK がその期間だけ結果をキャッシュする。
// http バックエンドは呼び出しごとに新しいセッションを張って即座にクローズする
// （withStatelessSession）。stdio バックエンドは共有セッションを使い回し、
// 失効時のみ invalidateSession で破棄・再接続する。
func (c *MCPBackendClient) ListTools(
	ctx context.Context,
	params *mcp.ListToolsParams,
) (_ *mcp.ListToolsResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/ListTools")
	defer func() { trace.EndSpan(ctx, rErr) }()

	if c.cfg.Transport == config.MCPTransportHTTP {
		var res *mcp.ListToolsResult
		err := c.withStatelessSession(ctx, func(session *mcp.ClientSession) (err error) {
			res, err = session.ListTools(ctx, params)
			return err
		})
		return res, err
	}

	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	res, err := session.ListTools(ctx, params)
	if isDeadSessionError(err) {
		c.invalidateSession(session)
	}
	return res, err
}

// CallTool はバックエンドへ tools/call をそのまま転送する。
// 引数の検証はバックエンド側の責務とし、ゲートウェイでは行わない。
// http バックエンドは ListTools と同様に呼び出しごとに新しいセッションで実行する。
func (c *MCPBackendClient) CallTool(
	ctx context.Context,
	name string,
	args json.RawMessage,
) (_ *mcp.CallToolResult, rErr error) {
	ctx = trace.StartSpan(ctx, "mcpsrv/MCPBackendClient/CallTool",
		attribute.String("tool-name", name))
	defer func() { trace.EndSpan(ctx, rErr) }()

	if c.cfg.Transport == config.MCPTransportHTTP {
		var res *mcp.CallToolResult
		err := c.withStatelessSession(ctx, func(session *mcp.ClientSession) (err error) {
			res, err = session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
			return err
		})
		return res, err
	}

	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if isDeadSessionError(err) {
		c.invalidateSession(session)
	}
	return res, err
}

// ListToolInfos はバックエンドの tools/list を全ページ問い合わせ、
// (name, description) の一覧を返す。http バックエンドは呼び出しごとに
// 新しいセッションで問い合わせる。
func (c *MCPBackendClient) ListToolInfos(ctx context.Context) ([]ToolInfo, error) {
	if c.cfg.Transport == config.MCPTransportHTTP {
		infos := []ToolInfo{}
		err := c.withStatelessSession(ctx, func(session *mcp.ClientSession) error {
			for tool, err := range session.Tools(ctx, nil) {
				if err != nil {
					return fmt.Errorf("backend %s: list tools: %w", c.name, err)
				}
				infos = append(infos, ToolInfo{Name: tool.Name, Description: tool.Description})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return infos, nil
	}

	session, err := c.ensureSession(ctx)
	if err != nil {
		return nil, err
	}
	infos := []ToolInfo{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			if isDeadSessionError(err) {
				c.invalidateSession(session)
			}
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
