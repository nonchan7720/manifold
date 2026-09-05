package config

import (
	"context"
	"fmt"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// CIMD 設定の既定値（oauth.cimd 省略時）。
const (
	DefaultCIMDCacheTTL = time.Hour

	DefaultCIMDMaxDocumentSize int64 = 65536
)

// OAuthConfig groups the gateway-wide settings for the OAuth 2.1
// authorization server Manifold exposes to downstream MCP clients.
type OAuthConfig struct {
	CIMD CIMDConfig `mapstructure:"cimd"`
}

func (c OAuthConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.CIMD),
	)
}

// CIMDConfig controls whether a downstream client may register itself by
// presenting an HTTPS client_id that resolves to a client ID metadata
// document (draft-ietf-oauth-client-id-metadata-document) instead of going
// through dynamic client registration. Disabled by default.
type CIMDConfig struct {
	Enabled bool `mapstructure:"enabled"`

	// AllowedOrigins, when non-empty, restricts CIMD client_id URLs to these
	// origins. It is applied before the document is fetched.
	AllowedOrigins []string `mapstructure:"allowedOrigins"`

	// CacheTTL caps how long a fetched document is reused. A shorter
	// Cache-Control max-age on the response wins.
	CacheTTL time.Duration `mapstructure:"cacheTTL"`

	// MaxDocumentSize is the maximum number of bytes read from a client ID
	// metadata document response.
	MaxDocumentSize int64 `mapstructure:"maxDocumentSize"`
}

// WithDefaults returns a copy of c with zero-value fields replaced by the
// documented defaults.
func (c CIMDConfig) WithDefaults() CIMDConfig {
	if c.CacheTTL == 0 {
		c.CacheTTL = DefaultCIMDCacheTTL
	}
	if c.MaxDocumentSize == 0 {
		c.MaxDocumentSize = DefaultCIMDMaxDocumentSize
	}
	return c
}

// AllowsOrigin reports whether origin passes the AllowedOrigins filter.
// An empty list allows every origin. Both sides are compared in the
// normalized form produced by NormalizeOrigin.
func (c CIMDConfig) AllowsOrigin(origin string) bool {
	if len(c.AllowedOrigins) == 0 {
		return true
	}
	normalized, err := NormalizeOrigin(origin)
	if err != nil {
		return false
	}
	for _, allowed := range c.AllowedOrigins {
		if a, err := NormalizeOrigin(allowed); err == nil && a == normalized {
			return true
		}
	}
	return false
}

func (c CIMDConfig) ValidateWithContext(ctx context.Context) error {
	if !c.Enabled {
		return nil
	}
	c = c.WithDefaults()
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.AllowedOrigins, validation.By(validateOrigins)),
		validation.Field(&c.CacheTTL, validation.By(validatePositiveDuration)),
		validation.Field(&c.MaxDocumentSize, validation.Min(int64(1))),
	)
}

func validateOrigins(value any) error {
	origins, ok := value.([]string)
	if !ok {
		return fmt.Errorf("type error: %T", value)
	}
	for _, origin := range origins {
		if _, err := NormalizeOrigin(origin); err != nil {
			return err
		}
	}
	return nil
}
