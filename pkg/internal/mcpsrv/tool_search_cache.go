package mcpsrv

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sync"
	"sync/atomic"
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

	// toolSearchIndexMaxBytes bounds the estimated size (see snapshotSize) of
	// everything one tool_search middleware keeps cached: each entry holds the
	// caller's full tool definitions with their inputSchemas, so a large
	// catalog times many callers would otherwise outgrow the entry cap's
	// intent. The budget is per middleware (one per server).
	toolSearchIndexMaxBytes = 64 << 20
)

// toolSearchSnapshot is what one read of a caller's visible tools yields: the
// search index over all of them, and the inner first tools/list page (for the
// pass-through below the threshold: its Cacheable, Meta and NextCursor).
type toolSearchSnapshot struct {
	index *toolsearch.Index
	first *mcp.ListToolsResult
}

// toolSearchIndexes caches, per caller, the authz-filtered tools tool_search
// searches together with their prebuilt search index, so a burst of tool_search
// calls and tools/list requests reads the inner tools/list pages (and runs
// authz / OPA for them) and tokenizes the catalog once rather than once per
// request. One instance serves one server (its middleware), so entries are
// keyed by caller alone.
type toolSearchIndexes struct {
	serverName string
	// authzKey, when set, identifies what the authz layer (which sits inside
	// tool_search) decides on beyond the bearer token: the principal from the
	// request headers and the bypass flag. It is part of the cache key, and a
	// request it can't derive a key for (authz would deny it) is never cached.
	authzKey AuthzCacheKeyer
	// toolCache supplies the invalidation generations (nil: TTL only). An
	// entry read before the server's tools were replaced
	// (ToolCache.InvalidateServer / InvalidateCaller) is dropped on its next
	// lookup, so a spec refresh or a rebuilt reverse server is seen at once.
	toolCache  *ToolCache
	ttl        time.Duration
	maxEntries int
	maxBytes   int
	now        func() time.Time

	// reservedWarned is set once the collision of a backend tool with the
	// synthetic tool_search has been logged, so the WARN isn't repeated on
	// every tools/list and tool_search call. It is cleared when a complete
	// read of the backend's tools no longer holds that tool, so adding it
	// again warns again.
	reservedWarned atomic.Bool

	mu      sync.Mutex
	entries map[string]*list.Element // value: *toolSearchIndexEntry
	// lru orders entries from most (front) to least (back) recently used.
	lru   *list.List
	bytes int
}

type toolSearchIndexEntry struct {
	key     string
	snap    *toolSearchSnapshot
	gen     uint64
	expires time.Time
	size    int
}

// snapshotSize estimates the memory a snapshot holds as the encoded size of
// its tool definitions and of the cached first page, computed once at insert.
// It ignores the lazily built BM25 tables, which scale with the same text.
func snapshotSize(snap *toolSearchSnapshot) int {
	size := 0
	if snap.index != nil {
		b, _ := json.Marshal(snap.index.Docs())
		size += len(b)
	}
	if snap.first != nil {
		b, _ := json.Marshal(snap.first)
		size += len(b)
	}
	return size
}

func newToolSearchIndexes(
	serverName string, toolCache *ToolCache, authzKey AuthzCacheKeyer,
) *toolSearchIndexes {
	return &toolSearchIndexes{
		serverName: serverName,
		authzKey:   authzKey,
		toolCache:  toolCache,
		ttl:        toolSearchIndexTTL,
		maxEntries: toolSearchIndexMaxEntries,
		maxBytes:   toolSearchIndexMaxBytes,
		now:        time.Now,
		entries:    map[string]*list.Element{},
		lru:        list.New(),
	}
}

// snapshotFor returns the search index (and inner first page) over the tools
// the caller of ctx can see, from the cache when a fresh entry for that caller exists and otherwise
// freshly read through next. The caller is identified like the tool cache does
// (cacheCaller), and a request without one, or one marked to bypass the tool
// cache, is never cached: it can't be told apart from other callers.
func (c *toolSearchIndexes) snapshotFor(
	ctx context.Context, next mcp.MethodHandler, req mcp.Request,
) (*toolSearchSnapshot, error) {
	read := func() (*toolSearchSnapshot, error) {
		first, tools, err := listVisibleTools(ctx, next, req)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(tools, func(t *mcp.Tool) bool { return t.Name == ToolSearchName }) {
			c.reservedWarned.Store(false)
		}
		return &toolSearchSnapshot{
			index: toolsearch.NewIndex(toolDefs(c.dropReservedTool(ctx, tools))),
			first: first,
		}, nil
	}
	_, identity, ok := cacheCaller(ctx)
	if !ok || toolCacheBypassed(ctx) {
		return read()
	}
	authzPart := ""
	if c.authzKey != nil {
		// authz が拒否する（主体を導出できない）リクエストはキャッシュ越しに
		// 返さず、authz まで通してポリシーエラーにする。
		part, ok := c.authzKey(req)
		if !ok {
			return read()
		}
		authzPart = part
	}
	scope := cacheScope{server: c.serverName, identity: identity}
	key := c.key(ctx, authzPart)
	gen := c.generation(scope)
	if snap, hit := c.get(key, gen); hit {
		return snap, nil
	}
	snap, err := read()
	if err != nil {
		return nil, err
	}
	// The tools may have been replaced while they were being read: store the
	// index only if the generation it was read under is still current.
	if c.generation(scope) == gen {
		c.set(key, gen, snap)
	}
	return snap, nil
}

func (c *toolSearchIndexes) generation(scope cacheScope) uint64 {
	if c.toolCache == nil {
		return 0
	}
	return c.toolCache.generation(scope)
}

// key hashes the caller (cacheCaller) with the server name, like toolCacheKey,
// and the authz principal key, so callers sharing a token but not a principal
// never share an index.
func (c *toolSearchIndexes) key(ctx context.Context, authzPart string) string {
	sum := sha256.Sum256(
		[]byte(toolCacheKey(ctx, c.serverName, "tool_search") + "\x00" + authzPart),
	)
	return hex.EncodeToString(sum[:])
}

func (c *toolSearchIndexes) get(key string, gen uint64) (*toolSearchSnapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := elem.Value.(*toolSearchIndexEntry)
	if !c.now().Before(entry.expires) || entry.gen != gen {
		c.removeLocked(elem)
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return entry.snap, true
}

// removeLocked drops elem (c.mu held by the caller).
func (c *toolSearchIndexes) removeLocked(elem *list.Element) {
	entry := c.lru.Remove(elem).(*toolSearchIndexEntry)
	delete(c.entries, entry.key)
	c.bytes -= entry.size
}

func (c *toolSearchIndexes) set(key string, gen uint64, snap *toolSearchSnapshot) {
	size := snapshotSize(snap)
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.entries[key]; ok {
		c.removeLocked(elem)
	}
	// 1 件で予算を超えるものは、他のエントリを追い出すだけなので保持しない。
	if size > c.maxBytes {
		return
	}
	c.entries[key] = c.lru.PushFront(&toolSearchIndexEntry{
		key: key, snap: snap, gen: gen, expires: c.now().Add(c.ttl), size: size,
	})
	c.bytes += size
	// 上限を超えたら、最も長く使われていないものから捨てる。
	for c.lru.Len() > c.maxEntries || c.bytes > c.maxBytes {
		c.removeLocked(c.lru.Back())
	}
}
