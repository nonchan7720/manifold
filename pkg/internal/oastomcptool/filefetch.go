package oastomcptool

import "sync"

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
}

var (
	fileFetchMu     sync.RWMutex
	fileFetchConfig = FileFetchConfig{MaxSize: defaultFileFetchMaxSize}
)

// SetFileFetchConfig installs the file-fetch policy used by fetchFileFromURL and
// writeMultipartFile. Call once at application startup, after loading config.
// A MaxSize <= 0 resets to the built-in default (500 MiB).
func SetFileFetchConfig(cfg FileFetchConfig) {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = defaultFileFetchMaxSize
	}
	fileFetchMu.Lock()
	defer fileFetchMu.Unlock()
	fileFetchConfig = cfg
}

// getFileFetchConfig returns the currently active file-fetch policy.
func getFileFetchConfig() FileFetchConfig {
	fileFetchMu.RLock()
	defer fileFetchMu.RUnlock()
	return fileFetchConfig
}
