package edge

import "context"

// identityKeyContextKey carries the identityKey derived for the current
// /mcp/{server_name} request (mcpAuthMiddleware) through to
// ReverseGateway.ResolveServer, which has no other way to learn which
// per-user mcp.Server to route to.
type identityKeyContextKey struct{}

// WithIdentityKey returns a copy of ctx carrying key as the request's
// identityKey.
func WithIdentityKey(ctx context.Context, key IdentityKey) context.Context {
	return context.WithValue(ctx, identityKeyContextKey{}, key)
}

// IdentityKeyFromContext returns the identityKey set by WithIdentityKey, if any.
func IdentityKeyFromContext(ctx context.Context) (IdentityKey, bool) {
	key, ok := ctx.Value(identityKeyContextKey{}).(IdentityKey)
	return key, ok
}
