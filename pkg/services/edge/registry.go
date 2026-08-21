// Package edge implements the WebMCP reverse-connection gateway services:
// the in-memory binding registry and the pairing/edge-token service.
package edge

import (
	"context"
	"sync"

	"github.com/nonchan7720/manifold/pkg/domain/edge"
)

type bindingEntry struct {
	binding edge.Binding
	handle  any
}

// InMemoryRegistry is the v1 implementation of edge.Registry: a single
// replica's live bindings held in memory. Phase 3 replaces this with a Redis
// implementation for cross-replica ownership (see docs/design/webmcp-reverse-gateway.ja.md).
type InMemoryRegistry struct {
	mu       sync.Mutex
	bindings map[string]*bindingEntry
}

// NewInMemoryRegistry creates an empty InMemoryRegistry.
func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{bindings: map[string]*bindingEntry{}}
}

func bindingKey(identityKey edge.IdentityKey, origin string) string {
	return string(identityKey) + "|" + origin
}

// Bind implements edge.Registry.
func (r *InMemoryRegistry) Bind(
	_ context.Context,
	binding edge.Binding,
	handle any,
) (previous any, hadPrevious bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := bindingKey(binding.IdentityKey, binding.Origin)
	prev, existed := r.bindings[key]
	r.bindings[key] = &bindingEntry{binding: binding, handle: handle}
	if existed {
		return prev.handle, true
	}
	return nil, false
}

// Unbind implements edge.Registry.
func (r *InMemoryRegistry) Unbind(
	_ context.Context,
	identityKey edge.IdentityKey,
	origin, appSession string,
) (handle any, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := bindingKey(identityKey, origin)
	entry, exists := r.bindings[key]
	if !exists || entry.binding.AppSession != appSession {
		return nil, false
	}
	delete(r.bindings, key)
	return entry.handle, true
}

// Resolve implements edge.Registry.
func (r *InMemoryRegistry) Resolve(
	_ context.Context,
	identityKey edge.IdentityKey,
	origin string,
) (handle any, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.bindings[bindingKey(identityKey, origin)]
	if !exists {
		return nil, false
	}
	return entry.handle, true
}

// DropConnection implements edge.Registry.
func (r *InMemoryRegistry) DropConnection(_ context.Context, connID string) []edge.DroppedBinding {
	r.mu.Lock()
	defer r.mu.Unlock()

	var dropped []edge.DroppedBinding
	for key, entry := range r.bindings {
		if entry.binding.ConnID != connID {
			continue
		}
		dropped = append(dropped, edge.DroppedBinding{Binding: entry.binding, Handle: entry.handle})
		delete(r.bindings, key)
	}
	return dropped
}
