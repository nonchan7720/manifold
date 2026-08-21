package config

import (
	"context"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type IdentitySource string

const (
	IdentitySourceJWT           IdentitySource = "jwt"
	IdentitySourceHeader        IdentitySource = "header"
	IdentitySourceIntrospection IdentitySource = "introspection"
)

// DefaultIdentityClaim is used when IdentityProfile.Claim is unset for a
// source: jwt profile.
const DefaultIdentityClaim = "sub"

// DefaultIntrospectionCacheTTL is used when IdentityProfile.CacheTTL is
// unset (<= 0) for a source: introspection profile.
const DefaultIntrospectionCacheTTL = 5 * time.Minute

// IdentityProfile is a named "how to derive an identityKey from an agent
// request" rule, referenced by reverse Server.Identity (see the "ユーザー識別
// （identity プロファイル）" section of docs/design/webmcp-reverse-gateway.ja.md).
// Only the fields for the selected Source are set; ValidateWithContext
// rejects fields belonging to another source as a config mistake.
type IdentityProfile struct {
	Source IdentitySource `mapstructure:"source"`

	// source: jwt. Claim defaults to DefaultIdentityClaim; Audience is optional.
	Claim    string `mapstructure:"claim"`
	Issuer   string `mapstructure:"issuer"`
	JWKSURL  string `mapstructure:"jwksURL"`
	Audience string `mapstructure:"audience"`

	// source: header
	Header string `mapstructure:"header"`
	Hash   bool   `mapstructure:"hash"`

	// source: introspection. CacheTTL defaults to DefaultIntrospectionCacheTTL.
	URL              string        `mapstructure:"url"`
	CredentialHeader string        `mapstructure:"credentialHeader"`
	CacheTTL         time.Duration `mapstructure:"cacheTTL"`
}

// ClaimOrDefault returns Claim, falling back to DefaultIdentityClaim when
// unset.
func (p IdentityProfile) ClaimOrDefault() string {
	if p.Claim == "" {
		return DefaultIdentityClaim
	}
	return p.Claim
}

// CacheTTLOrDefault returns CacheTTL, falling back to
// DefaultIntrospectionCacheTTL when unset (<= 0).
func (p IdentityProfile) CacheTTLOrDefault() time.Duration {
	if p.CacheTTL <= 0 {
		return DefaultIntrospectionCacheTTL
	}
	return p.CacheTTL
}

func (p IdentityProfile) ValidateWithContext(ctx context.Context) error {
	isJWT := p.Source == IdentitySourceJWT
	isHeader := p.Source == IdentitySourceHeader
	isIntrospection := p.Source == IdentitySourceIntrospection

	return validation.ValidateStructWithContext(
		ctx,
		&p,
		validation.Field(&p.Source,
			validation.Required,
			validation.In(IdentitySourceJWT, IdentitySourceHeader, IdentitySourceIntrospection),
		),

		validation.Field(&p.Issuer,
			validation.When(isJWT, validation.Required, is.RequestURL),
			validation.When(!isJWT, validation.Empty),
		),
		validation.Field(&p.JWKSURL,
			validation.When(isJWT, validation.Required, is.RequestURL),
			validation.When(!isJWT, validation.Empty),
		),
		validation.Field(&p.Audience, validation.When(!isJWT, validation.Empty)),
		validation.Field(&p.Claim, validation.When(!isJWT, validation.Empty)),

		validation.Field(&p.Header,
			validation.When(isHeader, validation.Required),
			validation.When(!isHeader, validation.Empty),
		),
		validation.Field(&p.Hash, validation.When(!isHeader, validation.Empty)),

		validation.Field(&p.URL,
			validation.When(isIntrospection, validation.Required, is.RequestURL),
			validation.When(!isIntrospection, validation.Empty),
		),
		validation.Field(&p.CredentialHeader,
			validation.When(isIntrospection, validation.Required),
			validation.When(!isIntrospection, validation.Empty),
		),
		validation.Field(&p.CacheTTL, validation.When(!isIntrospection, validation.Empty)),
	)
}

// identitiesContextKey carries Config.Identities into Server validation, so a
// reverse Server's Identity reference can be checked for existence without
// Server needing a Config reference (see edgeContextKey).
type identitiesContextKey struct{}
