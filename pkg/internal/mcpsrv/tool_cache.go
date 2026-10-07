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
	mu         sync.Mutex
	entries    map[string]toolCacheEntry
	maxEntries int
	now        func() time.Time
}

type toolCacheEntry struct {
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
		entries:    map[string]toolCacheEntry{},
		maxEntries: maxEntries,
		now:        time.Now,
	}
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

func (c *ToolCache) set(key string, value []byte, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.entries[key] = toolCacheEntry{value: value, expires: now.Add(ttl)}
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
func canonicalArguments(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
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
			if _, _, ok := cacheCaller(ctx); !ok {
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
				return cachedResult(ctx, cache, key, cfg.ToolsList, func() (mcp.Result, error) {
					return next(ctx, method, req)
				}, func() *mcp.ListToolsResult { return &mcp.ListToolsResult{} })
			case authzMethodToolsCall:
				params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
				if !ok || !cfg.CachesToolCall(params.Name) {
					return next(ctx, method, req)
				}
				key := toolCacheKey(ctx, server, method, params.Name,
					canonicalArguments(params.Arguments))
				return cachedResult(ctx, cache, key, cfg.ToolCall, func() (mcp.Result, error) {
					return next(ctx, method, req)
				}, func() *mcp.CallToolResult { return &mcp.CallToolResult{} })
			default:
				return next(ctx, method, req)
			}
		}
	}
}

// cachedResult serves key from cache, or calls fetch and stores its result
// when it succeeded (no error, and not a tool error for tools/call).
func cachedResult[R mcp.Result](
	ctx context.Context,
	cache *ToolCache,
	key string,
	ttl time.Duration,
	fetch func() (mcp.Result, error),
	newResult func() R,
) (mcp.Result, error) {
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
		cache.set(key, raw, ttl)
	}
	return res, nil
}
