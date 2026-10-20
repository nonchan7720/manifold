package config

import (
	"context"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// DefaultFileFetchMaxSize is used when FileFetchConfig.MaxSize is unset (<= 0).
// 500 MiB (524288000 bytes).
const DefaultFileFetchMaxSize int64 = 524288000

// FileFetchConfig controls how outbound file-fetch requests behave — e.g. when an
// MCP tool caller passes a URL for a file-input field and manifold downloads it on
// their behalf. This exists to mitigate SSRF: by default, downloads only allow
// https://, never connect to loopback/link-local (cloud metadata)/unspecified/
// multicast addresses, and refuse private ranges (RFC1918 etc.) unless the
// SKIP_SECURE_CLIENT environment variable is set (the official image sets it so
// signed URLs can point at in-cluster services).
type FileFetchConfig struct {
	// AllowLocal allows connecting to any IP address (including loopback) and
	// using the http:// scheme. Intended for local development/testing against a
	// local stack (e.g. ministack). Defaults to false.
	AllowLocal bool `mapstructure:"allowLocal"`

	// AllowedHosts, when non-empty, restricts URL downloads to these hosts only
	// (exact match against the URL host, with or without port). Empty means all
	// hosts are allowed, subject to the IP checks unless AllowLocal is true.
	AllowedHosts []string `mapstructure:"allowedHosts"`

	// DeniedHosts lists hosts (hostname, or host:port) URL downloads must never
	// reach. Checked after AllowedHosts, before the request and on every redirect.
	DeniedHosts []string `mapstructure:"deniedHosts"`

	// DeniedCIDRs lists CIDR blocks the download dialer must never connect to,
	// checked against the resolved IP of every connection regardless of
	// SKIP_SECURE_CLIENT. Use it to fence off internal services that are reachable
	// from the gateway but must not be reachable through tool callers.
	DeniedCIDRs []string `mapstructure:"deniedCIDRs"`

	// MaxSize is the maximum number of bytes accepted for a single file value,
	// whether downloaded from a URL or provided as base64/text content.
	// 0 (or unset) falls back to DefaultFileFetchMaxSize.
	MaxSize int64 `mapstructure:"maxSize"`
}

func (c FileFetchConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.DeniedCIDRs, validation.Each(validation.By(validateCIDR))),
	)
}

// WithDefaults returns a copy of c with zero-value fields replaced by defaults.
func (c FileFetchConfig) WithDefaults() FileFetchConfig {
	if c.MaxSize <= 0 {
		c.MaxSize = DefaultFileFetchMaxSize
	}
	return c
}
