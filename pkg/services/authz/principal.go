// Package authz decides whether a Principal may call a given MCP tool,
// delegating the decision to an OPA sidecar (see
// docs/design/opa-tool-authorization-plan.ja.md).
package authz

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nonchan7720/manifold/pkg/config"
)

// ErrMissingIdentity is returned when the request carries no usable user
// identity — no userID header, or no non-empty group after splitting.
// Callers must not query the Decider and must deny the request.
var ErrMissingIdentity = errors.New("authz: request is missing user identity")

// Principal is the caller identity forwarded to the OPA decision input,
// resolved from HTTP headers injected by an upstream identity layer.
type Principal struct {
	UserID string
	Groups []string
}

// splitGroups parses cfg.UserGroups' raw comma-separated value into a
// trimmed, non-empty group list. The values are treated as opaque strings.
func splitGroups(raw string) []string {
	parts := strings.Split(raw, ",")
	groups := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		groups = append(groups, p)
	}
	return groups
}

// BypassRequested reports whether h carries cfg.Bypass set to the literal
// string "true" — an exact, case-sensitive match, so anything else (missing,
// empty, "True", "1") leaves authz enforcement in effect.
func BypassRequested(h http.Header, cfg config.AuthzHeaders) bool {
	return h.Get(cfg.Bypass) == "true"
}

// PrincipalFromHeader derives a Principal from a single occurrence of
// cfg.UserID and cfg.UserGroups in h (repeated headers are not merged — only
// the first value is read, per http.Header.Get).
func PrincipalFromHeader(h http.Header, cfg config.AuthzHeaders) (Principal, error) {
	userID := h.Get(cfg.UserID)
	if userID == "" {
		return Principal{}, ErrMissingIdentity
	}
	groups := splitGroups(h.Get(cfg.UserGroups))
	if len(groups) == 0 {
		return Principal{}, ErrMissingIdentity
	}
	return Principal{UserID: userID, Groups: groups}, nil
}
