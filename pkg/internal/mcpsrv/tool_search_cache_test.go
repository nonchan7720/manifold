package mcpsrv

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
	"github.com/nonchan7720/manifold/pkg/services/authz"
	"github.com/stretchr/testify/require"
)

// callerTools is an inner tools/list handler serving a per-caller tool set
// (keyed by the bearer token) and counting its calls.
type callerTools struct {
	byToken map[string][]string
	calls   int
}

func (c *callerTools) handler(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
	c.calls++
	res := &mcp.ListToolsResult{}
	for _, name := range c.byToken[contexts.FromRequestAuthHeader(ctx)] {
		res.Tools = append(res.Tools, &mcp.Tool{
			Name: name, Description: name + " tool", InputSchema: map[string]any{"type": "object"},
		})
	}
	return res, nil
}

func withToken(ctx context.Context, token string) context.Context {
	return contexts.ToRequestAuthHeader(ctx, token)
}

func searchCalledNames(
	t *testing.T, ctx context.Context, h mcp.MethodHandler, query string,
) []string {
	t.Helper()
	res, err := h(ctx, authzMethodToolsCall, &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Name:      ToolSearchName,
			Arguments: []byte(`{"query":"` + query + `"}`),
		},
	})
	require.NoError(t, err)
	call, ok := res.(*mcp.CallToolResult)
	require.True(t, ok)
	return searchResultNames(t, call)
}

func TestToolSearch_Call_ReusesVisibleToolsForTheSameCaller(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"tok": {"list_pets", "get_pet"}}}
	h := newToolSearchMiddleware(
		"petstore", config.ToolSearchConfig{Enabled: true}, nil, nil,
	)(inner.handler)
	ctx := withToken(t.Context(), "tok")

	require.ElementsMatch(t, []string{"get_pet", "list_pets"}, searchCalledNames(t, ctx, h, "tool"))
	require.Equal(t, 1, inner.calls)
	require.Equal(t, []string{"get_pet"}, searchCalledNames(t, ctx, h, "get_pet"))
	require.Equal(t, 1, inner.calls, "second call must not re-read the inner tools/list")
}

func TestToolSearch_Call_NoCallerIdentityIsNotCached(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"": {"list_pets"}}}
	h := newToolSearchMiddleware(
		"petstore", config.ToolSearchConfig{Enabled: true}, nil, nil,
	)(inner.handler)

	searchCalledNames(t, t.Context(), h, "tool")
	searchCalledNames(t, t.Context(), h, "tool")
	require.Equal(t, 2, inner.calls)
}

func TestToolSearchIndexes_TTLExpiry(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"tok": {"list_pets"}}}
	c := newToolSearchIndexes("petstore", nil, nil)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := withToken(t.Context(), "tok")
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}

	_, err := c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	now = now.Add(toolSearchIndexTTL - time.Second)
	_, err = c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	require.Equal(t, 1, inner.calls)

	// 期限後は読み直し、新しいツールが見える。
	inner.byToken["tok"] = []string{"list_pets", "get_pet"}
	now = now.Add(2 * time.Second)
	index, err := c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls)
	require.Len(t, index.Docs(), 2)
}

func TestToolSearchIndexes_CallersAreIsolated(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{
		"alice": {"alice_tool"},
		"bob":   {"bob_tool"},
		"":      {"identity_tool"},
	}}
	c := newToolSearchIndexes("petstore", nil, nil)
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}
	docName := func(ctx context.Context) string {
		index, err := c.indexFor(ctx, inner.handler, req)
		require.NoError(t, err)
		require.Len(t, index.Docs(), 1)
		return index.Docs()[0].Name
	}

	require.Equal(t, "alice_tool", docName(withToken(t.Context(), "alice")))
	require.Equal(t, "bob_tool", docName(withToken(t.Context(), "bob")))
	// identityKey だけの呼び出し元（reverse エンドポイント）もトークンとは別枠。
	idCtx := domainedge.WithIdentityKey(t.Context(), domainedge.IdentityKey("alice"))
	require.Equal(t, "identity_tool", docName(idCtx))
	require.Equal(t, 3, inner.calls)

	require.Equal(t, "alice_tool", docName(withToken(t.Context(), "alice")))
	require.Equal(t, "bob_tool", docName(withToken(t.Context(), "bob")))
	require.Equal(t, "identity_tool", docName(idCtx))
	require.Equal(t, 3, inner.calls)
}

func TestToolSearchIndexes_InvalidatedWithToolCache(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"tok": {"list_pets"}}}
	toolCache := NewToolCache(0)
	c := newToolSearchIndexes("petstore", toolCache, nil)
	ctx := withToken(t.Context(), "tok")
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}

	_, err := c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	_, err = c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	require.Equal(t, 1, inner.calls)

	// 他サーバーの無効化では影響を受けない。
	toolCache.InvalidateServer("other")
	_, err = c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	require.Equal(t, 1, inner.calls)

	// spec リフレッシュ等でツールが差し替わったら TTL を待たずに読み直す。
	inner.byToken["tok"] = []string{"get_pet"}
	toolCache.InvalidateServer("petstore")
	index, err := c.indexFor(ctx, inner.handler, req)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls)
	require.Equal(t, "get_pet", index.Docs()[0].Name)
}

func TestToolSearchIndexes_BoundedSize(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{}}
	c := newToolSearchIndexes("petstore", nil, nil)
	c.maxEntries = 3
	req := &mcp.ListToolsRequest{Params: &mcp.ListToolsParams{}}
	for _, tok := range []string{"a", "b", "c", "d", "e"} {
		inner.byToken[tok] = []string{tok}
		_, err := c.indexFor(withToken(t.Context(), tok), inner.handler, req)
		require.NoError(t, err)
	}
	require.LessOrEqual(t, len(c.entries), 3)
}

// groupDecider allows every tool to the "operators" group and only listpets to
// anyone else.
type groupDecider struct{ fakeDecider }

func (d *groupDecider) AllowedTools(
	_ context.Context, p authz.Principal, tools []authz.ToolRef,
) ([]authz.ToolRef, error) {
	if slices.Contains(p.Groups, "operators") {
		return tools, nil
	}
	var out []authz.ToolRef
	for _, t := range tools {
		if t.Name == "listpets" {
			out = append(out, t)
		}
	}
	return out, nil
}

func (d *groupDecider) Allow(
	_ context.Context, p authz.Principal, t authz.ToolRef,
) (bool, error) {
	return slices.Contains(p.Groups, "operators") || t.Name == "listpets", nil
}

// authzSearchHandler is tool_search (with the authz keyer) in front of the
// real authz middleware, like ServerToolMiddlewares orders them.
func authzSearchHandler(t *testing.T, inner *callerTools) mcp.MethodHandler {
	t.Helper()
	headers := testAuthzHeaders()
	chain := []mcp.Middleware{
		newToolSearchMiddleware(
			"petstore", config.ToolSearchConfig{Enabled: true}, nil,
			NewAuthzCacheKeyer(headers, nil),
		),
		NewAuthzMiddleware("petstore", "petstore", &groupDecider{}, headers, nil),
	}
	h := inner.handler
	for i := len(chain) - 1; i >= 0; i-- {
		h = chain[i](h)
	}
	return h
}

func searchRequest(header http.Header) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{
			Name: ToolSearchName, Arguments: []byte(`{"query":"tool"}`),
		},
		Extra: &mcp.RequestExtra{Header: header},
	}
}

func principalHeader(user, groups string) http.Header {
	h := http.Header{}
	if user != "" {
		h.Set("x-user-id", user)
	}
	if groups != "" {
		h.Set("x-user-groups", groups)
	}
	return h
}

func TestToolSearch_Call_SameTokenDifferentGroupsDoNotShareIndex(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"tok": {"listpets", "deletepet"}}}
	h := authzSearchHandler(t, inner)
	ctx := withToken(t.Context(), "tok")

	res, err := h(ctx, authzMethodToolsCall, searchRequest(principalHeader("u1", "operators")))
	require.NoError(t, err)
	call, ok := res.(*mcp.CallToolResult)
	require.True(t, ok)
	require.ElementsMatch(t, []string{"deletepet", "listpets"}, searchResultNames(t, call))

	res, err = h(ctx, authzMethodToolsCall, searchRequest(principalHeader("u2", "readers")))
	require.NoError(t, err)
	call, ok = res.(*mcp.CallToolResult)
	require.True(t, ok)
	require.Equal(t, []string{"listpets"}, searchResultNames(t, call))

	// Same principal again is still served from cache.
	before := inner.calls
	_, err = h(ctx, authzMethodToolsCall, searchRequest(principalHeader("u2", "readers")))
	require.NoError(t, err)
	require.Equal(t, before, inner.calls)
}

func TestToolSearch_Call_MissingIdentityAfterCachedHitIsDeniedByPolicy(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"tok": {"listpets"}}}
	h := authzSearchHandler(t, inner)
	ctx := withToken(t.Context(), "tok")

	_, err := h(ctx, authzMethodToolsCall, searchRequest(principalHeader("u1", "operators")))
	require.NoError(t, err)

	_, err = h(ctx, authzMethodToolsCall, searchRequest(http.Header{}))
	require.ErrorIs(t, err, errToolNotAllowedByPolicy)
}

func TestToolSearch_AuthzMiddlewaresWithoutKeyerAreNotCached(t *testing.T) {
	inner := &callerTools{byToken: map[string][]string{"tok": {"listpets"}}}
	allow := func(next mcp.MethodHandler) mcp.MethodHandler { return next }
	h := mcp.MethodHandler(inner.handler)
	chain := ServerToolMiddlewares(
		"petstore", nil, []mcp.Middleware{allow}, nil, nil, nil,
		config.ToolSearchConfig{Enabled: true},
	)
	for i := len(chain) - 1; i >= 0; i-- {
		h = chain[i](h)
	}
	ctx := withToken(t.Context(), "tok")
	searchCalledNames(t, ctx, h, "pet")
	searchCalledNames(t, ctx, h, "pet")
	require.Equal(t, 2, inner.calls)
}

func TestNewAuthzCacheKeyer(t *testing.T) {
	fh := map[string]config.AuthzInputHeaderField{"tenant": {Header: "x-tenant"}}
	k := NewAuthzCacheKeyer(testAuthzHeaders(), fh)
	key := func(h http.Header) (string, bool) { return k(searchRequest(h)) }

	h := principalHeader("u1", "a,b")
	h.Set("x-tenant", "t1")
	k1, ok := key(h)
	require.True(t, ok)
	k1b, _ := key(h)
	require.Equal(t, k1, k1b)

	h2 := principalHeader("u1", "a,b")
	h2.Set("x-tenant", "t2")
	k2, ok := key(h2)
	require.True(t, ok)
	require.NotEqual(t, k1, k2, "extra fields are part of the key")

	_, ok = key(principalHeader("", "a"))
	require.False(t, ok, "missing identity")
	_, ok = k(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}})
	require.False(t, ok, "no HTTP extra")

	bh := http.Header{}
	bh.Set("x-authz-bypass", "true")
	kb, ok := key(bh)
	require.True(t, ok)
	require.NotEqual(t, k1, kb)
}
