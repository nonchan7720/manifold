package mcpsrv

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestToolCacheMiddleware(t *testing.T) {
	var calls atomic.Int32
	srv := newToolTestServer(t, &calls, "getpet", "addpet", "fails")
	cache := NewToolCache(0)
	srv.AddReceivingMiddleware(newToolCacheMiddleware("petstore", &config.CacheConfig{
		ToolsList: time.Minute,
		ToolCall:  time.Minute,
		Tools:     []string{"get*", "fails"},
	}, cache))

	alice := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "alice"), srv)
	bob := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "bob"), srv)

	// 同じ引数（キー順違い）は 1 回だけバックエンドへ届く
	require.Equal(
		t,
		`getpet:{"a":1,"b":2}`,
		callText(t, alice, "getpet", map[string]any{"a": 1, "b": 2}),
	)
	require.Equal(t, `getpet:{"a":1,"b":2}`,
		callText(t, alice, "getpet", json.RawMessage(`{"b":2, "a":1}`)))
	require.Equal(t, int32(1), calls.Load())

	// 別の引数・別の呼び出し元はキャッシュを共有しない
	callText(t, alice, "getpet", map[string]any{"a": 2})
	callText(t, bob, "getpet", map[string]any{"a": 1, "b": 2})
	require.Equal(t, int32(3), calls.Load())

	// tools に一致しないツールはキャッシュしない
	callText(t, alice, "addpet", map[string]any{})
	callText(t, alice, "addpet", map[string]any{})
	require.Equal(t, int32(5), calls.Load())

	// ツールのエラー結果はキャッシュしない
	for range 2 {
		res, err := alice.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: "fails", Arguments: map[string]any{}},
		)
		require.NoError(t, err)
		require.True(t, res.IsError)
	}
	require.Equal(t, int32(7), calls.Load())

	// tools/list もキャッシュされ、ヒット時も同じ内容を返す
	before := cache.Len()
	require.ElementsMatch(t, []string{"getpet", "addpet", "fails"}, sessionToolNames(t, alice))
	require.Equal(t, before+1, cache.Len())
	require.ElementsMatch(t, []string{"getpet", "addpet", "fails"}, sessionToolNames(t, alice))
	require.Equal(t, before+1, cache.Len())
}

// reverse（WebMCP）サーバーは JWT ミドルウェアを通らず bearer が空で、
// identityKey だけが呼び出し元を区別する。キャッシュもそれで分かれること。
func TestToolCacheMiddleware_SeparatesIdentityKeys(t *testing.T) {
	var calls atomic.Int32
	srv := newToolTestServer(t, &calls, "getpet")
	cache := NewToolCache(0)
	srv.AddReceivingMiddleware(newToolCacheMiddleware("webmcp", &config.CacheConfig{
		ToolsList: time.Minute,
		ToolCall:  time.Minute,
		Tools:     []string{"getpet"},
	}, cache))

	alice := connectTestClient(
		t, domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("alice")), srv,
	)
	bob := connectTestClient(
		t, domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("bob")), srv,
	)

	callText(t, alice, "getpet", map[string]any{"id": 1})
	callText(t, alice, "getpet", map[string]any{"id": 1})
	require.Equal(t, int32(1), calls.Load(), "same identityKey hits the cache")
	callText(t, bob, "getpet", map[string]any{"id": 1})
	require.Equal(t, int32(2), calls.Load(), "another identityKey never sees alice's result")

	sessionToolNames(t, alice)
	before := cache.Len()
	sessionToolNames(t, bob)
	require.Equal(t, before+1, cache.Len(), "tools/list is cached per identityKey too")

	// bearer と identityKey は別スロット: 同じ文字列でも衝突しない
	require.NotEqual(t,
		toolCacheKey(contexts.ToRequestAuthHeader(t.Context(), "x"), "s"),
		toolCacheKey(domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("x")), "s"),
	)
}

// bearer も identityKey も無い呼び出し元は区別できないため、キャッシュしない。
func TestToolCacheMiddleware_SkipsAnonymousCaller(t *testing.T) {
	var calls atomic.Int32
	srv := newToolTestServer(t, &calls, "getpet")
	cache := NewToolCache(0)
	srv.AddReceivingMiddleware(newToolCacheMiddleware("petstore", &config.CacheConfig{
		ToolsList: time.Minute,
		ToolCall:  time.Minute,
		Tools:     []string{"getpet"},
	}, cache))
	cs := connectTestClient(t, t.Context(), srv)

	callText(t, cs, "getpet", map[string]any{"id": 1})
	callText(t, cs, "getpet", map[string]any{"id": 1})
	sessionToolNames(t, cs)
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, 0, cache.Len())
}

func TestToolCache_ExpiryAndBound(t *testing.T) {
	cache := NewToolCache(2)
	now := time.Unix(0, 0)
	cache.now = func() time.Time { return now }

	cache.set(cacheScope{server: "s"}, "a", 0, []byte("1"), time.Second)
	got, ok := cache.get("a")
	require.True(t, ok)
	require.Equal(t, []byte("1"), got)

	now = now.Add(time.Second)
	_, ok = cache.get("a")
	require.False(t, ok, "entry must expire after its TTL")

	cache.set(cacheScope{server: "s"}, "a", 0, []byte("1"), time.Minute)
	cache.set(cacheScope{server: "s"}, "b", 0, []byte("2"), time.Minute)
	cache.set(cacheScope{server: "s"}, "c", 0, []byte("3"), time.Minute)
	require.Equal(t, 2, cache.Len(), "cache must stay within maxEntries")
	_, ok = cache.get("c")
	require.True(t, ok, "the newest entry is always stored")
}

func TestToolCache_InvalidateServer(t *testing.T) {
	cache := NewToolCache(0)
	petstore := cacheScope{server: "petstore"}
	alice := cacheScope{server: "petstore", identity: "alice"}
	other := cacheScope{server: "other"}
	require.True(t, cache.set(petstore, "a", 0, []byte("1"), time.Minute))
	require.True(t, cache.set(alice, "b", 0, []byte("2"), time.Minute))
	require.True(t, cache.set(other, "c", 0, []byte("3"), time.Minute))

	gen, aliceGen := cache.generation(petstore), cache.generation(alice)
	cache.InvalidateServer("petstore")
	require.Equal(
		t,
		1,
		cache.Len(),
		"every entry of the named server is dropped, other servers stay",
	)
	_, ok := cache.get("a")
	require.False(t, ok)
	_, ok = cache.get("b")
	require.False(t, ok)
	_, ok = cache.get("c")
	require.True(t, ok)

	// 無効化前に取得を始めた（= 古い世代の）結果は、サーバー全体でも
	// 呼び出し元単位のスコープでも登録されない
	require.False(t, cache.set(petstore, "d", gen, []byte("4"), time.Minute))
	require.False(t, cache.set(alice, "e", aliceGen, []byte("5"), time.Minute))
	require.Equal(t, 1, cache.Len())
	require.True(t, cache.set(petstore, "d", cache.generation(petstore), []byte("4"), time.Minute))
	require.True(t, cache.set(alice, "e", cache.generation(alice), []byte("5"), time.Minute))
	require.Equal(t, 3, cache.Len())
	require.Equal(t, uint64(0), cache.generation(other), "other servers keep their generation")

	cache.InvalidateServer("unknown") // 存在しないサーバーでも何も起きない
	require.Equal(t, 3, cache.Len())
}

// reverse の per-user サーバーの再構築は、その identityKey のエントリだけを捨て、
// 同じサーバーの他ユーザーのエントリと、サーバー全体の世代には触れない。
func TestToolCache_InvalidateCaller(t *testing.T) {
	cache := NewToolCache(0)
	alice := cacheScope{server: "webmcp", identity: "alice"}
	bob := cacheScope{server: "webmcp", identity: "bob"}
	require.True(t, cache.set(alice, "a", 0, []byte("1"), time.Minute))
	require.True(t, cache.set(bob, "b", 0, []byte("2"), time.Minute))

	aliceGen, bobGen := cache.generation(alice), cache.generation(bob)
	cache.InvalidateCaller("webmcp", "alice")
	_, ok := cache.get("a")
	require.False(t, ok, "alice's entry is dropped")
	_, ok = cache.get("b")
	require.True(t, ok, "bob's entry survives")

	require.False(t, cache.set(alice, "a", aliceGen, []byte("1"), time.Minute),
		"alice's in-flight result from before the rebuild is not stored")
	require.Equal(t, bobGen, cache.generation(bob), "bob's generation is unchanged")
	require.True(t, cache.set(bob, "c", bobGen, []byte("3"), time.Minute))
	require.Equal(t, uint64(0), cache.generation(cacheScope{server: "webmcp"}),
		"the whole-server generation is unchanged")
}

// 取得の途中で InvalidateServer が走った場合、その取得結果（更新前のもの）を
// 完了時に再登録して TTL の間返し続けないこと。
func TestToolCacheMiddleware_InvalidationDuringFetchIsNotUndone(t *testing.T) {
	var (
		calls   atomic.Int32
		entered sync.Once
	)
	enteredCh := make(chan struct{})
	release := make(chan struct{})
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.1"}, nil)
	srv.AddTool(
		&mcp.Tool{Name: "getpet", InputSchema: map[string]any{"type": "object"}},
		func(_ context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			entered.Do(func() { close(enteredCh) })
			<-release
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: "pet"}},
			}, nil
		},
	)
	cache := NewToolCache(0)
	srv.AddReceivingMiddleware(newToolCacheMiddleware("petstore", &config.CacheConfig{
		ToolCall: time.Minute,
		Tools:    []string{"getpet"},
	}, cache))
	cs := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "alice"), srv)

	done := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(
			t.Context(),
			&mcp.CallToolParams{Name: "getpet", Arguments: map[string]any{}},
		)
		done <- err
	}()
	<-enteredCh
	cache.InvalidateServer("petstore") // 取得中にツールが差し替わった
	close(release)
	require.NoError(t, <-done)
	require.Equal(
		t,
		0,
		cache.Len(),
		"the result fetched before the invalidation must not be stored",
	)

	callText(t, cs, "getpet", map[string]any{})
	require.Equal(
		t,
		int32(2),
		calls.Load(),
		"the next call fetches again and caches the fresh result",
	)
	require.Equal(t, 1, cache.Len())
}

// 2^53 を超える整数が float64 に丸められて別の引数が同じキーに衝突しないこと。
func TestCanonicalArguments_PreservesLargeIntegers(t *testing.T) {
	a := canonicalArguments(json.RawMessage(`{"id": 9007199254740993}`))
	b := canonicalArguments(json.RawMessage(`{"id": 9007199254740992}`))
	require.NotEqual(t, a, b)
	require.Equal(t, `{"id":9007199254740993}`, a)

	// キー順と空白の違いは引き続き同一視する
	require.Equal(t,
		canonicalArguments(json.RawMessage(`{"b": 2, "a": 1}`)),
		canonicalArguments(json.RawMessage(`{"a":1,"b":2}`)),
	)
	require.Equal(t, "", canonicalArguments(json.RawMessage(` `)))
	require.Equal(t, "not json", canonicalArguments(json.RawMessage("not json")))
}

func TestToolCacheMiddleware_LargeIntegerArgumentsDoNotCollide(t *testing.T) {
	var calls atomic.Int32
	srv := newToolTestServer(t, &calls, "get_order")
	srv.AddReceivingMiddleware(newToolCacheMiddleware("orders", &config.CacheConfig{
		ToolCall: time.Minute,
		Tools:    []string{"get_order"},
	}, NewToolCache(0)))
	cs := connectTestClient(t, contexts.ToRequestAuthHeader(t.Context(), "alice"), srv)

	first := callText(t, cs, "get_order", json.RawMessage(`{"id": 9007199254740993}`))
	second := callText(t, cs, "get_order", json.RawMessage(`{"id": 9007199254740992}`))
	require.NotEqual(t, first, second, "a different id must not get the cached order")
	require.Equal(t, int32(2), calls.Load())
}

func TestToolCacheMiddleware_NilWhenNothingCached(t *testing.T) {
	require.Nil(t, newToolCacheMiddleware("s", nil, NewToolCache(0)))
	require.Nil(t, newToolCacheMiddleware("s", &config.CacheConfig{ToolsList: time.Minute}, nil))
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
	}, nil, logger)...)
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
