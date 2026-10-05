package mcpsrv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/nonchan7720/manifold/pkg/internal/toolsearch"
)

const (
	// toolSearchIndexTTL is how long a caller's visible tools (and the search
	// index built from them) are reused by tool_search calls. Short on purpose:
	// a policy or backend change that no invalidation hook covers shows up
	// within this time.
	toolSearchIndexTTL = 30 * time.Second

	// toolSearchIndexMaxEntries bounds the callers one tool_search middleware
	// keeps an index for.
	toolSearchIndexMaxEntries = 1000
)

// toolSearchIndexes caches, per caller, the authz-filtered tools tool_search
// searches together with their prebuilt search index, so a burst of tool_search
// calls reads the inner tools/list pages (and runs authz / OPA for them) and
// tokenizes the catalog once rather than once per call. One instance serves one
// server (its middleware), so entries are keyed by caller alone.
type toolSearchIndexes struct {
	serverName string
	// toolCache supplies the invalidation generations (nil: TTL only). An
	// entry read before the server's tools were replaced
	// (ToolCache.InvalidateServer / InvalidateCaller) is dropped on its next
	// lookup, so a spec refresh or a rebuilt reverse server is seen at once.
	toolCache  *ToolCache
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.Mutex
	entries map[string]toolSearchIndexEntry
}

type toolSearchIndexEntry struct {
	index   *toolsearch.Index
	gen     uint64
	expires time.Time
}

func newToolSearchIndexes(serverName string, toolCache *ToolCache) *toolSearchIndexes {
	return &toolSearchIndexes{
		serverName: serverName,
		toolCache:  toolCache,
		ttl:        toolSearchIndexTTL,
		maxEntries: toolSearchIndexMaxEntries,
		now:        time.Now,
		entries:    map[string]toolSearchIndexEntry{},
	}
}

// indexFor returns the search index over the tools the caller of ctx can see,
// from the cache when a fresh entry for that caller exists and otherwise
// freshly read through next. The caller is identified like the tool cache does
// (cacheCaller), and a request without one, or one marked to bypass the tool
// cache, is never cached: it can't be told apart from other callers.
func (c *toolSearchIndexes) indexFor(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (*toolsearch.Index, error) {
	read := func() (*toolsearch.Index, error) {
		_, tools, err := listVisibleTools(ctx, next, req)
		if err != nil {
			return nil, err
		}
		return toolsearch.NewIndex(toolDefs(dropReservedTool(ctx, c.serverName, tools))), nil
	}
	_, identity, ok := cacheCaller(ctx)
	if !ok || toolCacheBypassed(ctx) {
		return read()
	}
	scope := cacheScope{server: c.serverName, identity: identity}
	key := c.key(ctx)
	gen := c.generation(scope)
	if index, hit := c.get(key, gen); hit {
		return index, nil
	}
	index, err := read()
	if err != nil {
		return nil, err
	}
	// The tools may have been replaced while they were being read: store the
	// index only if the generation it was read under is still current.
	if c.generation(scope) == gen {
		c.set(key, gen, index)
	}
	return index, nil
}

func (c *toolSearchIndexes) generation(scope cacheScope) uint64 {
	if c.toolCache == nil {
		return 0
	}
	return c.toolCache.generation(scope)
}

// key hashes the caller (cacheCaller) with the server name, like toolCacheKey.
func (c *toolSearchIndexes) key(ctx context.Context) string {
	sum := sha256.Sum256([]byte(toolCacheKey(ctx, c.serverName, "tool_search")))
	return hex.EncodeToString(sum[:])
}

func (c *toolSearchIndexes) get(key string, gen uint64) (*toolsearch.Index, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if !c.now().Before(entry.expires) || entry.gen != gen {
		delete(c.entries, key)
		return nil, false
	}
	return entry.index, true
}

func (c *toolSearchIndexes) set(key string, gen uint64, index *toolsearch.Index) {
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
	c.entries[key] = toolSearchIndexEntry{index: index, gen: gen, expires: now.Add(c.ttl)}
}
