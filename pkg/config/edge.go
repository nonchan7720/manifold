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

	// TrustCloudflare, when true, additionally trusts Cloudflare's published
	// edge IP ranges as /edge/pair rate-limit forwarders. Only enable this
	// when Manifold is actually deployed behind Cloudflare — see
	// docs/design/webmcp-reverse-gateway-phase2.ja.md「Phase 1 からの持ち越し判断事項」.
	TrustCloudflare bool `mapstructure:"trustCloudflare"`

	// TrustedForwarders adds extra CIDR prefixes to trust as /edge/pair
	// rate-limit forwarders, alongside the RFC1918 default and (if enabled)
	// Cloudflare's ranges — e.g. an ALB/Ingress subnet outside RFC1918.
	TrustedForwarders []string `mapstructure:"trustedForwarders"`
}

// WithDefaults returns a copy of c with zero-value fields replaced by the
// documented defaults (pairing/remote — forwardAuth remains config structure
// only, see docs/design/webmcp-reverse-gateway.ja.md「拡張と identity の紐づけ」).
func (c EdgeConfig) WithDefaults() EdgeConfig {
	if c.Auth == "" {
		c.Auth = EdgeAuthPairing
	}
	if c.Pairing.Type == "" {
		c.Pairing.Type = PairingTypeRemote
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
		validation.Field(&c.TrustedForwarders, validation.Each(validation.By(validateCIDR))),
	)
}

func validateCIDR(value any) error {
	s, _ := value.(string)
	if _, _, err := net.ParseCIDR(s); err != nil {
		return fmt.Errorf("must be a valid CIDR: %w", err)
	}
	return nil
}

func (c PairingConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Type, validation.In(PairingTypeRemote, PairingTypeStatic)),
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
