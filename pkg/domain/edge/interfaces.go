// Package edge holds the domain model for the WebMCP reverse-connection
// gateway: the state that tracks which browser tab (per user, per origin) is
// currently reachable through an edge WebSocket connection.
//
// See docs/design/webmcp-reverse-gateway.ja.md for the full design.
package edge

import "context"

// IdentityKey identifies a single end user across the reverse mcpServers that
// share an identity profile. For pairing+static deployments the key is fixed
// (see StaticIdentityKey), since requests carry no stable per-user credential
// to derive one from.
type IdentityKey string

// StaticIdentityKey is the identityKey used by pairing+static deployments.
const StaticIdentityKey IdentityKey = "static"

// Binding is one (IdentityKey, Origin) pair's currently live app connection.
// AppSession changes every time the tab reconnects or reloads; ConnID
// identifies the owning edge WebSocket connection, so a disconnect can drop
// every Binding it owns in one pass.
type Binding struct {
	IdentityKey IdentityKey
	Origin      string
	AppSession  string
	ConnID      string
}

// DroppedBinding pairs a Binding with the opaque handle it was bound to, for
// bulk teardown after DropConnection or a superseding Bind.
type DroppedBinding struct {
	Binding Binding
	Handle  any
}

// Registry tracks which (IdentityKey, Origin) pairs currently have a live
// WebMCP tab connected through the edge WebSocket, and resolves tool calls to
// an opaque per-binding handle. The handle's concrete type is owned by the
// mcpsrv layer (the reverse mcp.Transport connection and per-user mcp.Server
// built from it); the registry only tracks its lifecycle.
type Registry interface {
	// Bind records binding as the current generation for
	// (binding.IdentityKey, binding.Origin), replacing (last-writer-wins) any
	// previous generation. The previously bound handle, if any, is returned so
	// the caller can tear it down.
	Bind(ctx context.Context, binding Binding, handle any) (previous any, hadPrevious bool)

	// Unbind removes the binding if appSession still matches the currently
	// bound generation, returning its handle.
	Unbind(
		ctx context.Context,
		identityKey IdentityKey,
		origin, appSession string,
	) (handle any, ok bool)

	// Resolve returns the handle bound to (identityKey, origin), if a tab is
	// currently connected.
	Resolve(ctx context.Context, identityKey IdentityKey, origin string) (handle any, ok bool)

	// DropConnection removes every binding owned by connID (WS disconnect),
	// returning their handles so in-flight calls can be resolved as failed.
	DropConnection(ctx context.Context, connID string) []DroppedBinding
}
