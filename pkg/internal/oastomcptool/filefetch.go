package oastomcptool

import (
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/nonchan7720/manifold/pkg/internal/client"
)

// defaultFileFetchMaxSize is used when FileFetchConfig.MaxSize is unset (<= 0).
// 500 MiB (524288000 bytes) — mirrors config.DefaultFileFetchMaxSize.
const defaultFileFetchMaxSize int64 = 524288000

// FileFetchConfig controls SSRF-related behavior when oastomcptool fetches file
// content from a URL supplied by a tool caller (e.g. {"file": "https://..."} or
// {"file": {"url": "https://..."}}).
//
// This mirrors config.FileFetchConfig field-for-field. oastomcptool does not
// import pkg/config directly: pkg/config pulls in viper/ozzo-validation and other
// app-wiring dependencies that this low-level, independently-testable package has
// no other reason to depend on, and importing it would invert the intended
// layering (pkg/config is meant to sit above internal packages, configuring them,
// not the other way around). The application copies values over once at startup
// via SetFileFetchConfig, after loading config.
type FileFetchConfig struct {
	// AllowLocal, when true, allows connecting to private/loopback/link-local IPs
	// and using the http:// scheme — intended only for local development/tests
	// against a local stack (e.g. ministack). Defaults to false.
	AllowLocal bool
	// AllowedHosts, when non-empty, restricts URL downloads to these hosts only
	// (exact match against the URL host, with or without port).
	AllowedHosts []string
	// MaxSize caps the number of bytes accepted for a single file value
	// (URL download or base64/text content). <= 0 falls back to defaultFileFetchMaxSize.
	MaxSize int64
	// DeniedHosts lists hosts (hostname, or host:port) that URL downloads must never
	// reach, checked before the request and on every redirect hop. Applied after
	// AllowedHosts.
	DeniedHosts []string
	// DeniedCIDRs lists CIDR blocks that the dialer must never connect to, checked
	// against the resolved IP of every connection. Entries must parse with
	// net.ParseCIDR; SetFileFetchConfig returns an error otherwise.
	DeniedCIDRs []string
}

var (
	fileFetchMu     sync.RWMutex
	fileFetchConfig = FileFetchConfig{MaxSize: defaultFileFetchMaxSize}
	// fileFetchClient is the client used by fetchFileFromURL when AllowLocal is
	// false. It is (re)built by SetFileFetchConfig so DeniedCIDRs are compiled into
	// its dialer once; see client.NewCallerURLClient for the dial policy.
	fileFetchClient = client.NewCallerURLClient(nil)
)

// SetFileFetchConfig installs the file-fetch policy used by fetchFileFromURL and
// writeMultipartFile, and builds the HTTP client that enforces it. Call once at
// application startup, after loading config. A MaxSize <= 0 resets to the
// built-in default (500 MiB). Slices are cloned before storing, so the caller's
// slices can never be mutated later to bypass the lock (symmetric with
// getFileFetchConfig, which also returns copies). An unparsable DeniedCIDRs entry
// is returned as an error and leaves the previous policy in place.
func SetFileFetchConfig(cfg FileFetchConfig) error {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = defaultFileFetchMaxSize
	}
	cfg.AllowedHosts = append([]string(nil), cfg.AllowedHosts...)
	cfg.DeniedHosts = append([]string(nil), cfg.DeniedHosts...)
	cfg.DeniedCIDRs = append([]string(nil), cfg.DeniedCIDRs...)
	denied := make([]*net.IPNet, 0, len(cfg.DeniedCIDRs))
	for _, raw := range cfg.DeniedCIDRs {
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return fmt.Errorf("fileFetch.deniedCIDRs: %q is not a valid CIDR: %w", raw, err)
		}
		denied = append(denied, n)
	}
	c := client.NewCallerURLClient(denied)
	fileFetchMu.Lock()
	defer fileFetchMu.Unlock()
	fileFetchConfig = cfg
	fileFetchClient = c
	return nil
}

// getFileFetchClient returns the client SetFileFetchConfig built for
// AllowLocal=false downloads.
func getFileFetchClient() *http.Client {
	fileFetchMu.RLock()
	defer fileFetchMu.RUnlock()
	return fileFetchClient
}

// getFileFetchConfig returns the currently active file-fetch policy. AllowedHosts
// is returned as a copy so callers can never mutate the shared global slice's
// backing array.
func getFileFetchConfig() FileFetchConfig {
	fileFetchMu.RLock()
	defer fileFetchMu.RUnlock()
	cfg := fileFetchConfig
	if cfg.AllowedHosts != nil {
		cfg.AllowedHosts = append([]string{}, cfg.AllowedHosts...)
	}
	if cfg.DeniedHosts != nil {
		cfg.DeniedHosts = append([]string{}, cfg.DeniedHosts...)
	}
	if cfg.DeniedCIDRs != nil {
		cfg.DeniedCIDRs = append([]string{}, cfg.DeniedCIDRs...)
	}
	return cfg
}
