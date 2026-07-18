package config

// DefaultFileFetchMaxSize is used when FileFetchConfig.MaxSize is unset (<= 0).
// 500 MiB (524288000 bytes).
const DefaultFileFetchMaxSize int64 = 524288000

// FileFetchConfig controls how outbound file-fetch requests behave — e.g. when an
// MCP tool caller passes a URL for a file-input field and manifold downloads it on
// their behalf. This exists to mitigate SSRF: by default, downloads only allow
// https:// and refuse to connect to private/loopback/link-local IP addresses.
type FileFetchConfig struct {
	// AllowLocal allows connecting to private/loopback/link-local IP addresses and
	// using the http:// scheme. Intended for local development/testing against a
	// local stack (e.g. ministack). Defaults to false.
	AllowLocal bool `mapstructure:"allowLocal"`

	// AllowedHosts, when non-empty, restricts URL downloads to these hosts only
	// (exact match against the URL host, with or without port). Empty means all
	// hosts are allowed, subject to the private-IP block unless AllowLocal is true.
	AllowedHosts []string `mapstructure:"allowedHosts"`

	// MaxSize is the maximum number of bytes accepted for a single file value,
	// whether downloaded from a URL or provided as base64/text content.
	// 0 (or unset) falls back to DefaultFileFetchMaxSize.
	MaxSize int64 `mapstructure:"maxSize"`
}

// WithDefaults returns a copy of c with zero-value fields replaced by defaults.
func (c FileFetchConfig) WithDefaults() FileFetchConfig {
	if c.MaxSize <= 0 {
		c.MaxSize = DefaultFileFetchMaxSize
	}
	return c
}
