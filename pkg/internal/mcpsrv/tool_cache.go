package mcpsrv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/internal/contexts"
)

// DefaultToolCacheMaxEntries bounds the number of results a ToolCache holds
// across all servers.
const DefaultToolCacheMaxEntries = 10000

// ToolCache is the in-process store behind mcpServers.<name>.cache, shared by
// every server. Entries are kept as JSON so a hit always hands out a fresh
// copy: outer middlewares (authz) modify the results they receive.
type ToolCache struct {
	mu      sync.Mutex
	entries map[string]toolCacheEntry
	// generations counts InvalidateServer calls per server. cachedResult
	// reads a server's generation before fetching and set refuses a result
	// fetched under an older one, so a fetch that was in flight when the
	// server's tools were replaced can't re-register the stale result.
	generations map[string]uint64
	maxEntries  int
	now         func() time.Time
}

type toolCacheEntry struct {
	// server is the mcpServers entry the result belongs to, so
	// InvalidateServer can drop a server's entries without knowing their keys.
	server  string
	value   []byte
	expires time.Time
}

// NewToolCache returns an empty ToolCache holding at most maxEntries results
// (DefaultToolCacheMaxEntries when maxEntries <= 0).
func NewToolCache(maxEntries int) *ToolCache {
	if maxEntries <= 0 {
		maxEntries = DefaultToolCacheMaxEntries
	}
	return &ToolCache{
		entries:     map[string]toolCacheEntry{},
		generations: map[string]uint64{},
		maxEntries:  maxEntries,
		now:         time.Now,
	}
}

// generation returns server's current invalidation generation, to pass to
// set with a result fetched from now on.
func (c *ToolCache) generation(server string) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generations[server]
}

func (c *ToolCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !c.now().Before(entry.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return entry.value, true
}

// set stores value for key under server, unless gen (server's generation
// when the value was fetched, see generation) is no longer current: the
// server's tools were replaced while the fetch was in flight, so the value
// is stale and is dropped. It reports whether the value was stored.
func (c *ToolCache) set(server, key string, gen uint64, value []byte, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generations[server] != gen {
		return false
	}
	now := c.now()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		for k, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, k)
			}
		}
		// 期限切れが無ければ任意の 1 件を捨てて上限を守る。
		for k := range c.entries {
			if len(c.entries) < c.maxEntries {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = toolCacheEntry{server: server, value: value, expires: now.Add(ttl)}
	return true
}

// InvalidateServer drops every entry cached for server (its tools/list pages
// and tools/call results, for every caller) and starts a new generation for
// it, so a result fetched before the call is neither served nor stored
// afterwards. Call it whenever the server's tools are replaced underneath
// the cache — a spec refresh adopting a new spec, or a reverse (WebMCP)
// per-user server being rebuilt.
func (c *ToolCache) InvalidateServer(server string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, entry := range c.entries {
		if entry.server == server {
			delete(c.entries, k)
		}
	}
	c.generations[server]++
}

type toolCacheBypassKey struct{}

// withToolCacheBypass marks ctx so newToolCacheMiddleware neither serves nor
// stores results for the request: used for a request still being handled by
// a reverse per-user server that has since been replaced (see
// retiredServerMiddleware), whose results describe the previous tab.
func withToolCacheBypass(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolCacheBypassKey{}, true)
}

func toolCacheBypassed(ctx context.Context) bool {
	v, _ := ctx.Value(toolCacheBypassKey{}).(bool)
	return v
}

// Len returns the number of entries currently held (expired ones included
// until they are next touched).
func (c *ToolCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// cacheCaller identifies the caller a cached result belongs to: the bearer
// token middleware.JWT stored (every non-reverse endpoint) and the identityKey
// mcpAuthMiddleware resolved for a reverse (WebMCP) endpoint, which skips the
// JWT middleware and so has no token. Both are kept in their own slot so an
// empty one can't collide with the other. ok is false when neither is set:
// such a caller can't be told apart from any other, so nothing is cached for
// it (see newToolCacheMiddleware).
func cacheCaller(ctx context.Context) (token, identity string, ok bool) {
	token = contexts.FromRequestAuthHeader(ctx)
	if key, found := domainedge.IdentityKeyFromContext(ctx); found {
		identity = string(key)
	}
	return token, identity, token != "" || identity != ""
}

// toolCacheKey hashes the parts identifying a cached result. The caller
// (cacheCaller) is part of every key, so a result fetched with one caller's
// credentials, or in one user's browser tab, is never served to another.
func toolCacheKey(ctx context.Context, parts ...string) string {
	token, identity, _ := cacheCaller(ctx)
	h := sha256.New()
	for _, part := range append([]string{"token:" + token, "identity:" + identity}, parts...) {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalArguments re-encodes tool arguments so that semantically equal
// JSON objects (different key order or whitespace) share a cache entry.
// Numbers are kept verbatim (json.Number) rather than going through float64,
// so integers beyond 2^53 — ids, for instance — that differ in their last
// digits never collapse into the same key.
func canonicalArguments(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return string(raw)
	}
	out, err := json.Marshal(v) // map のキーはソートされて出力される
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// newToolCacheMiddleware returns the middleware caching server's tools/list
// and (for the tools cfg.Tools names) tools/call results, or nil when cfg
// caches nothing. It sits inside authz, so every call is still authorized
// before a cached result is returned, and outside the tool filter, so it
// caches the exposed names. Only successful results are cached, and only for
// a caller the gateway can identify (cacheCaller): a request carrying neither
// a bearer token nor an identityKey bypasses the cache rather than sharing
// entries with every other such request.
func newToolCacheMiddleware(
	server string,
	cfg *config.CacheConfig,
	cache *ToolCache,
) mcp.Middleware {
	if cache == nil || cfg == nil || (!cfg.CachesToolsList() && cfg.ToolCall <= 0) {
		return nil
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if _, _, ok := cacheCaller(ctx); !ok || toolCacheBypassed(ctx) {
				return next(ctx, method, req)
			}
			switch method {
			case authzMethodToolsList:
				if !cfg.CachesToolsList() {
					return next(ctx, method, req)
				}
				cursor := ""
				if params, ok := req.GetParams().(*mcp.ListToolsParams); ok && params != nil {
					cursor = params.Cursor
				}
				key := toolCacheKey(ctx, server, method, cursor)
				return cachedResult(
					ctx,
					cache,
					server,
					key,
					cfg.ToolsList,
					func() (mcp.Result, error) {
						return next(ctx, method, req)
					},
					func() *mcp.ListToolsResult { return &mcp.ListToolsResult{} },
				)
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok || !cfg.CachesToolCall(params.Name) {
					return next(ctx, method, req)
				}
				key := toolCacheKey(ctx, server, method, params.Name,
					canonicalArguments(params.Arguments))
				return cachedResult(
					ctx,
					cache,
					server,
					key,
					cfg.ToolCall,
					func() (mcp.Result, error) {
						return next(ctx, method, req)
					},
					func() *mcp.CallToolResult { return &mcp.CallToolResult{} },
				)
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// cachedResult serves key from cache, or calls fetch and stores its result
// under server when it succeeded (no error, and not a tool error for
// tools/call) and server's tools weren't replaced meanwhile (see
// ToolCache.set).
func cachedResult[R mcp.Result](
	ctx context.Context,
	cache *ToolCache,
	server, key string,
	ttl time.Duration,
	fetch func() (mcp.Result, error),
	newResult func() R,
) (mcp.Result, error) {
	gen := cache.generation(server)
	if raw, ok := cache.get(key); ok {
		res := newResult()
		if err := json.Unmarshal(raw, res); err == nil {
			return res, nil
		}
		slog.WarnContext(ctx, "tool cache: dropping undecodable entry")
	}
	res, err := fetch()
	if err != nil {
		return nil, err
	}
	if callRes, ok := res.(*mcp.CallToolResult); ok && callRes.IsError {
		return res, nil
	}
	if raw, err := json.Marshal(res); err == nil {
		cache.set(server, key, gen, raw, ttl)
	}
	return res, nil
}
