package contexts

import (
	"context"

	"github.com/nonchan7720/manifold/pkg/config"
)

type headerContextKey struct{}

func FromHeaderContext(ctx context.Context) map[string][]string {
	v, _ := ctx.Value(headerContextKey{}).(map[string][]string)
	return v
}

func ToHeaderContext(ctx context.Context, v map[string][]string) context.Context {
	return context.WithValue(ctx, headerContextKey{}, v)
}

type authHeaderKey struct{}

func FromRequestAuthHeader(ctx context.Context) string {
	v, _ := ctx.Value(authHeaderKey{}).(string)
	return v
}

func ToRequestAuthHeader(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, authHeaderKey{}, value)
}

type tokenClaimsKey struct{}

// FromTokenClaims returns the claims of the downstream Bearer token that
// mcpServers.<name>.auth.jwt verified, or nil when the token was not verified.
func FromTokenClaims(ctx context.Context) map[string]any {
	v, _ := ctx.Value(tokenClaimsKey{}).(map[string]any)
	return v
}

func ToTokenClaims(ctx context.Context, v map[string]any) context.Context {
	return context.WithValue(ctx, tokenClaimsKey{}, v)
}

type serverContextKey struct{}

func FromServerContext(ctx context.Context) *config.Server {
	v, _ := ctx.Value(serverContextKey{}).(*config.Server)
	return v
}

func ToServerContext(ctx context.Context, v *config.Server) context.Context {
	return context.WithValue(ctx, serverContextKey{}, v)
}
