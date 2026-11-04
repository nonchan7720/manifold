package oastomcptool

import (
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

// URL 由来のファイル取得（{"file": "https://..."} や {"file": {"url": "https://..."}}）の
// SSRF 対策ポリシーは config.FileFetchConfig をそのまま使う。以前はこのパッケージに
// フィールドが同一の FileFetchConfig を複製していたが、oastomcptool は pkg/internal/client
// 経由で既に pkg/config に依存しているため、複製する理由がない。

var (
	fileFetchMu     sync.RWMutex
	fileFetchConfig = config.FileFetchConfig{}.WithDefaults()
	// fileFetchClient is the client used by fetchFileFromURL when AllowLocal is
	// false. It is (re)built by SetFileFetchConfig so DeniedCIDRs are compiled into
	// its dialer once; see client.NewCallerURLClient for the dial policy.
	fileFetchClient = client.NewCallerURLClient(nil)
)

// SetFileFetchConfig installs the file-fetch policy used by fetchFileFromURL and
// writeMultipartFile, and builds the HTTP client that enforces it. Call once at
// application startup, after loading config. A MaxSize <= 0 resets to
// config.DefaultFileFetchMaxSize (500 MiB). Slices are cloned before storing, so the caller's
// slices can never be mutated later to bypass the lock (symmetric with
// getFileFetchConfig, which also returns copies). An unparsable DeniedCIDRs entry
// is returned as an error and leaves the previous policy in place.
func SetFileFetchConfig(cfg config.FileFetchConfig) error {
	cfg = cfg.WithDefaults()
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
func getFileFetchConfig() config.FileFetchConfig {
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
