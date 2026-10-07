package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/stretchr/testify/require"
)

// newToolTestServer returns a server with the named tools; each tool answers
// with its own name and counts its calls in calls.
func newToolTestServer(t *testing.T, calls *atomic.Int32, names ...string) *mcp.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	for _, name := range names {
		srv.AddTool(
			&mcp.Tool{
				Name:        name,
				Description: "the " + name + " tool",
				InputSchema: map[string]any{"type": "object"},
			},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				if calls != nil {
					calls.Add(1)
				}
				if req.Params.Name == "fails" {
					res := &mcp.CallToolResult{}
					res.SetError(errBoom)
					return res, nil
				}
				return &mcp.CallToolResult{
					Content: []mcp.Content{&mcp.TextContent{
						Text: req.Params.Name + ":" + string(req.Params.Arguments),
					}},
				}, nil
			},
		)
	}
	return srv
}

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }

// connectTestClient connects a client to srv in memory. ctx is the context
// the server session handles requests with.
func connectTestClient(t *testing.T, ctx context.Context, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "caller", Version: "0.0.1"}, nil)
	cs, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func sessionToolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}
	return names
}

func callText(t *testing.T, cs *mcp.ClientSession, name string, args any) string {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool %s returned an error", name)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return text.Text
}

func TestToolFilterMiddleware(t *testing.T) {
	srv := newToolTestServer(t, nil, "getpet", "addpet", "deletepet", "get_pet_v2", "listdocs")
	srv.AddReceivingMiddleware(newToolFilterMiddleware(&config.ToolsConfig{
		Include: []string{"get*", "addpet", "listdocs", "deletepet"},
		Exclude: []string{"delete*"},
		Overrides: map[string]config.ToolOverride{
			"getpet": {Name: "get_pet_v2", Description: "Look up a pet"},
			"addpet": {Description: "Create a pet"},
		},
	}))
	cs := connectTestClient(t, t.Context(), srv)

	res, err := cs.ListTools(t.Context(), nil)
	require.NoError(t, err)
	got := map[string]string{}
	for _, tool := range res.Tools {
		got[tool.Name] = tool.Description
	}
	// getpet は get_pet_v2 へリネームされ、元の get_pet_v2 は隠れる。deletepet は除外。
	require.Equal(t, map[string]string{
		"get_pet_v2": "Look up a pet",
		"addpet":     "Create a pet",
		"listdocs":   "the listdocs tool",
	}, got)

	// リネーム後の名前で元のツールが呼ばれる
	require.Equal(t, "getpet:{}", callText(t, cs, "get_pet_v2", map[string]any{}))
	require.Equal(t, "addpet:{}", callText(t, cs, "addpet", map[string]any{}))

	for _, hidden := range []string{"getpet", "deletepet"} {
		_, err := cs.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: hidden, Arguments: map[string]any{}},
		)
		require.ErrorContains(t, err, "unknown tool", hidden)
	}

	// SDK が保持する登録済みツールそのものは書き換えない
	unfiltered := connectTestClient(t, t.Context(), newToolTestServer(t, nil, "getpet"))
	require.Equal(t, []string{"getpet"}, sessionToolNames(t, unfiltered))
}

// ゲートウェイ自身が登録するツール（reverse サーバーの create_pairing_code）は
// include / exclude / overrides の対象外で、常に元の名前で見え、呼べること。
func TestToolFilterMiddleware_BuiltinToolsBypassFilter(t *testing.T) {
	srv := newToolTestServer(t, nil, "read_dom", "write_dom", createPairingCodeToolName)
	srv.AddReceivingMiddleware(newToolFilterMiddleware(&config.ToolsConfig{
		Include: []string{"read_*"},
		Overrides: map[string]config.ToolOverride{
			createPairingCodeToolName: {Name: "pair", Description: "ignored"},
		},
	}, createPairingCodeToolName))
	cs := connectTestClient(t, t.Context(), srv)

	require.ElementsMatch(
		t, []string{"read_dom", createPairingCodeToolName}, sessionToolNames(t, cs),
	)
	require.Equal(
		t,
		createPairingCodeToolName+":{}",
		callText(t, cs, createPairingCodeToolName, map[string]any{}),
	)
	for _, hidden := range []string{"write_dom", "pair"} {
		_, err := cs.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: hidden, Arguments: map[string]any{}},
		)
		require.ErrorContains(t, err, "unknown tool", hidden)
	}
}

// 組み込みツールは overrides で別名を付けられない（authz は公開名を見るため、
// 別名を許可するポリシーで create_pairing_code を呼べてしまう）。また、
// バックエンドのツールを組み込みツールの名前へリネームすると、組み込み側が
// その名前を持ち続け、リネームした側は隠れる。
func TestToolFilterMiddleware_BuiltinToolsCannotBeAliasedOrShadowed(t *testing.T) {
	srv := newToolTestServer(t, nil, "read_dom", "write_dom", createPairingCodeToolName)
	srv.AddReceivingMiddleware(newToolFilterMiddleware(&config.ToolsConfig{
		Overrides: map[string]config.ToolOverride{
			createPairingCodeToolName: {Name: "pair"},
			"write_dom":               {Name: createPairingCodeToolName},
		},
	}, createPairingCodeToolName))
	cs := connectTestClient(t, t.Context(), srv)

	require.ElementsMatch(
		t, []string{"read_dom", createPairingCodeToolName}, sessionToolNames(t, cs),
	)
	// 組み込みツールはその名前で呼べ、別名では呼べない
	require.Equal(
		t,
		createPairingCodeToolName+":{}",
		callText(t, cs, createPairingCodeToolName, map[string]any{}),
	)
	for _, hidden := range []string{"pair", "write_dom"} {
		_, err := cs.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: hidden, Arguments: map[string]any{}},
		)
		require.ErrorContains(t, err, "unknown tool", hidden)
	}
}

func TestToolFilterMiddleware_NilWithoutSettings(t *testing.T) {
	require.Nil(t, newToolFilterMiddleware(nil))
	require.Nil(t, newToolFilterMiddleware(&config.ToolsConfig{File: "x.yaml"}))
}

func TestToolFilter_ApplyInfos(t *testing.T) {
	f := newToolFilter(&config.ToolsConfig{
		Exclude:   []string{"secret"},
		Overrides: map[string]config.ToolOverride{"a": {Name: "renamed", Description: "new"}},
	})
	got := f.applyInfos([]ToolInfo{
		{Name: "a", Description: "old"},
		{Name: "secret"},
		{Name: "b", Description: "kept"},
	})
	require.Equal(t, []ToolInfo{
		{Name: "renamed", Description: "new"},
		{Name: "b", Description: "kept"},
	}, got)
	require.Equal(
		t,
		[]ToolInfo{{Name: "x"}},
		(*toolFilter)(nil).applyInfos([]ToolInfo{{Name: "x"}}),
	)
}

func decodeAuditLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &m))
		out = append(out, m)
	}
	return out
}

func TestAuditMiddleware(t *testing.T) {
	srv := newToolTestServer(t, nil, "getpet", "fails")
	var buf bytes.Buffer
	logger := newAuditLoggerTo(
		&buf,
		config.AuthzHeaders{UserID: "X-User-Id", UserGroups: "X-User-Groups"},
		true,
	)
	srv.AddReceivingMiddleware(newAuditMiddleware("petstore", "pets", logger))
	cs := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "secret-token"), srv)

	callText(t, cs, "getpet", map[string]any{"id": 1})
	res, err := cs.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "fails", Arguments: map[string]any{}},
	)
	require.NoError(t, err)
	require.True(t, res.IsError)
	_, err = cs.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "missing", Arguments: map[string]any{}},
	)
	require.Error(t, err)
	sessionToolNames(t, cs) // tools/list は記録しない

	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 3)
	require.Equal(t, "audit", lines[0]["msg"])
	require.Equal(t, "tool_call", lines[0]["event"])
	require.Equal(t, "petstore", lines[0]["server"])
	require.Equal(t, "pets", lines[0]["service"])
	require.Equal(t, "getpet", lines[0]["tool"])
	require.Equal(t, AuditOutcomeSuccess, lines[0]["outcome"])
	require.Equal(t, map[string]any{"id": float64(1)}, lines[0]["arguments"])
	require.Equal(t, tokenFingerprint("secret-token"), lines[0]["token"])
	require.NotContains(t, buf.String(), "secret-token")
	require.Equal(t, AuditOutcomeToolError, lines[1]["outcome"])
	require.Equal(t, AuditOutcomeError, lines[2]["outcome"])
	require.Contains(t, lines[2]["error"], "unknown tool")
}

func TestAuditMiddleware_RecordsDenial(t *testing.T) {
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, config.AuthzHeaders{}, false)
	deny := func(mcp.MethodHandler) mcp.MethodHandler {
		return func(context.Context, string, mcp.Request) (mcp.Result, error) {
			return nil, errToolNotAllowedByPolicy
		}
	}
	srv := newToolTestServer(t, nil, "getpet")
	srv.AddReceivingMiddleware(ServerToolMiddlewares("petstore", nil, []mcp.Middleware{
		func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				if method == authzMethodToolsCall {
					return deny(next)(ctx, method, req)
				}
				return next(ctx, method, req)
			}
		},
	}, logger)...)
	cs := connectTestClient(t, t.Context(), srv)
	_, err := cs.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "getpet", Arguments: map[string]any{"id": 1}},
	)
	require.Error(t, err)

	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 1)
	require.Equal(t, AuditOutcomeDenied, lines[0]["outcome"])
	require.NotContains(
		t,
		lines[0],
		"arguments",
		"arguments are only recorded when includeArguments is set",
	)
}

func TestNewAuditLogger(t *testing.T) {
	path := t.TempDir() + "/audit.jsonl"
	l, err := NewAuditLogger(config.AuditConfig{Enabled: true, Output: path}, config.AuthzHeaders{})
	require.NoError(t, err)
	require.NotNil(t, l)
	require.NoError(t, l.Close())
}

// reverse（WebMCP）のエンドポイントは JWT を検証せず user / groups / token が
// 空になるため、監査ログには identityKey が呼び出し元として残ること。
func TestAuditMiddleware_RecordsIdentityKey(t *testing.T) {
	srv := newToolTestServer(t, nil, "getpet")
	var buf bytes.Buffer
	logger := newAuditLoggerTo(&buf, config.AuthzHeaders{}, false)
	srv.AddReceivingMiddleware(newAuditMiddleware("app", "app", logger))

	ctx := domainedge.WithIdentityKey(t.Context(), "oauth:user-a")
	cs := connectTestClient(t, ctx, srv)
	callText(t, cs, "getpet", map[string]any{"id": 1})

	lines := decodeAuditLines(t, &buf)
	require.Len(t, lines, 1)
	require.Equal(t, "oauth:user-a", lines[0]["identity"])
	require.NotContains(t, lines[0], "token")
	require.NotContains(t, lines[0], "user")

	// identityKey の無い（bearer トークンの）エンドポイントでは出力しない。
	buf.Reset()
	cs = connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "tok"), srv)
	callText(t, cs, "getpet", map[string]any{"id": 1})
	lines = decodeAuditLines(t, &buf)
	require.Len(t, lines, 1)
	require.NotContains(t, lines[0], "identity")
}
