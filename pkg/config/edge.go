package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type EdgeAuth string

const (
	EdgeAuthPairing     EdgeAuth = "pairing"
	EdgeAuthForwardAuth EdgeAuth = "forwardAuth"
)

type PairingType string

const (
	PairingTypeRemote PairingType = "remote"
	PairingTypeStatic PairingType = "static"
)

type PairingConfig struct {
	Type PairingType `mapstructure:"type"`
}

// EdgeConfig selects how the reverse-connection browser extension binds its
// WebSocket connection to an identityKey (see docs/design/webmcp-reverse-gateway.ja.md).
type EdgeConfig struct {
	Auth    EdgeAuth      `mapstructure:"auth"`
	Pairing PairingConfig `mapstructure:"pairing"`
}

// WithDefaults returns a copy of c with zero-value fields replaced by the
// documented defaults (pairing/static — remote pairing and forwardAuth are
// config structure only in Phase 1, see docs/design/webmcp-reverse-gateway.ja.md).
func (c EdgeConfig) WithDefaults() EdgeConfig {
	if c.Auth == "" {
		c.Auth = EdgeAuthPairing
	}
	if c.Pairing.Type == "" {
		c.Pairing.Type = PairingTypeStatic
	}
	return c
}

// IsStaticPairing reports whether this deployment binds the edge connection
// to a fixed identityKey instead of deriving one per agent request.
func (c EdgeConfig) IsStaticPairing() bool {
	return c.Pairing.Type == PairingTypeStatic
}

func (c EdgeConfig) ValidateWithContext(ctx context.Context) error {
	c = c.WithDefaults()
	return validation.ValidateStructWithContext(ctx, &c,
		// forwardAuth is not implemented yet (config structure only); see
		// docs/design/webmcp-reverse-gateway.ja.md.
		validation.Field(&c.Auth, validation.In(EdgeAuthPairing)),
		validation.Field(&c.Pairing),
	)
}

func (c PairingConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		// remote pairing is not implemented yet (config structure only); see
		// docs/design/webmcp-reverse-gateway.ja.md.
		validation.Field(&c.Type, validation.In(PairingTypeStatic)),
	)
}

// NormalizeOrigin validates that raw is a bare origin (scheme + host, no
// path/query/fragment/userinfo) and returns it with a lowercased scheme and
// host. Only http/https are accepted.
func NormalizeOrigin(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("origin is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid origin %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("origin %q must use http or https", raw)
	}
	if u.Host == "" {
		return "", fmt.Errorf("origin %q must include a host", raw)
	}
	if u.User != nil {
		return "", fmt.Errorf("origin %q must not contain userinfo", raw)
	}
	if path := u.EscapedPath(); path != "" && path != "/" {
		return "", fmt.Errorf("origin %q must not contain a path", raw)
	}
	if u.RawQuery != "" {
		return "", fmt.Errorf("origin %q must not contain a query", raw)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("origin %q must not contain a fragment", raw)
	}
	return scheme + "://" + strings.ToLower(stripDefaultPort(scheme, u.Host)), nil
}

// stripDefaultPort removes the port from host when it numerically equals the
// scheme's default (80 for http, 443 for https), matching how browsers
// serialize location.origin — including zero-padded forms such as ":0443"
// (the extension's app.up/ready origin comparisons rely on this to line up).
func stripDefaultPort(scheme, host string) string {
	defaultPort, ok := map[string]int{"http": 80, "https": 443}[scheme]
	if !ok {
		return host
	}
	h, portStr, err := net.SplitHostPort(host)
	if err != nil {
		return host
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port != defaultPort {
		return host
	}
	if strings.Contains(h, ":") {
		return "[" + h + "]"
	}
	return h
}
