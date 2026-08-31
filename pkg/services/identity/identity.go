// Package identity resolves a domainedge.IdentityKey from an AI agent's
// inbound HTTP request, per the identities profile referenced by a reverse
// Server (see the "ユーザー識別（identity プロファイル）" section of
// docs/design/webmcp-reverse-gateway.ja.md, implemented in Phase 2a).
package identity

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
)

// Resolver derives the identityKey for a request under a single identity profile.
type Resolver interface {
	Resolve(ctx context.Context, req *http.Request) (domainedge.IdentityKey, error)
}

// encodeIdentityKey serializes an (profile, value) tuple into a
// domainedge.IdentityKey. Each component is base64url-encoded independently
// before being joined with ":" — a byte the base64url alphabet never
// produces — so no (profile, value) pair can ever collide with a different
// pair, regardless of ':' or other delimiter-like bytes inside profile or
// value. The result also always contains ':', so it can never equal
// domainedge.StaticIdentityKey ("static").
func encodeIdentityKey(profile, value string) domainedge.IdentityKey {
	return domainedge.IdentityKey(
		base64.RawURLEncoding.EncodeToString([]byte(profile)) + ":" +
			base64.RawURLEncoding.EncodeToString([]byte(value)),
	)
}

// NewResolver builds the Resolver for a single named profile. encryptKey is
// gateway.encryptKey (decoded, 32 bytes) and is only used by source: header
// profiles with hash: true.
func NewResolver(
	ctx context.Context,
	profileName string,
	profile *config.IdentityProfile,
	encryptKey []byte,
) (_ Resolver, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/NewResolver")
	defer func() { trace.EndSpan(ctx, rErr) }()

	switch profile.Source {
	case config.IdentitySourceJWT:
		return newJWTResolver(ctx, profileName, profile)
	case config.IdentitySourceHeader:
		return newHeaderResolver(profileName, profile, encryptKey)
	case config.IdentitySourceIntrospection:
		return newIntrospectionResolver(profileName, profile), nil
	default:
		return nil, fmt.Errorf(
			"identity: profile %q: unknown source %q", profileName, profile.Source,
		)
	}
}

// NewResolvers builds a Resolver for every entry in profiles, keyed by
// profile name.
func NewResolvers(
	ctx context.Context,
	profiles map[string]*config.IdentityProfile,
	encryptKey []byte,
) (_ map[string]Resolver, rErr error) {
	ctx = trace.StartSpan(ctx, "identity/NewResolvers")
	defer func() { trace.EndSpan(ctx, rErr) }()

	resolvers := make(map[string]Resolver, len(profiles))
	for name, profile := range profiles {
		r, err := NewResolver(ctx, name, profile, encryptKey)
		if err != nil {
			return nil, err
		}
		resolvers[name] = r
	}
	return resolvers, nil
}
