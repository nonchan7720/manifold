// Package authz decides whether a Principal may call a given MCP tool,
// delegating the decision to an OPA sidecar (see
// docs/design/opa-tool-authorization-plan.ja.md).
package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/nonchan7720/manifold/pkg/config"
)

// Sentinels distinguishing why a request could not be turned into a
// Principal, so an operator reading the deny log can tell a missing identity
// header from a missing tenant header from a malformed value. Callers must
// not query the Decider and must deny the request for any of them.
var (
	// ErrMissingIdentity covers the identity headers only — no userID
	// header, or no non-empty group after splitting.
	ErrMissingIdentity = errors.New("authz: request is missing user identity")

	// ErrMissingInputHeader covers a required config.AuthzInput.FromHeaders
	// field whose header is absent or empty.
	ErrMissingInputHeader = errors.New("authz: request is missing a required input header")

	// ErrInvalidInputHeader covers a config.AuthzInput.FromHeaders value
	// that does not parse as its configured type.
	ErrInvalidInputHeader = errors.New("authz: input header value does not match its type")
)

// Reason labels for the deny log, one per sentinel above plus a catch-all.
const (
	DenyReasonMissingIdentity    = "missing_identity"
	DenyReasonMissingInputHeader = "missing_input_header"
	DenyReasonInvalidInputHeader = "invalid_input_header"
	DenyReasonPrincipalError     = "principal_error"
)

// Principal is the caller identity forwarded to the OPA decision input,
// resolved from HTTP headers injected by an upstream identity layer.
type Principal struct {
	UserID string
	Groups []string

	// Extra holds the fields resolved from config.AuthzInput.FromHeaders,
	// keyed by decision-input field name. Empty when fromHeaders is unset
	// or when every configured field is optional and absent.
	Extra map[string]any
}

// DenyReason classifies a PrincipalFromHeader error into a stable label for
// the "authz decision" log, so the reason is greppable without parsing the
// message (which carries the field and header names).
func DenyReason(err error) string {
	switch {
	case errors.Is(err, ErrMissingIdentity):
		return DenyReasonMissingIdentity
	case errors.Is(err, ErrMissingInputHeader):
		return DenyReasonMissingInputHeader
	case errors.Is(err, ErrInvalidInputHeader):
		return DenyReasonInvalidInputHeader
	default:
		return DenyReasonPrincipalError
	}
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

// resolveInputHeaderValue converts h's spec.Header value to spec.Type's Go
// representation. It reports ok=false when the header carries no usable
// value (absent, empty, or a list whose every element was blank), leaving
// the required/optional decision to the caller. A number is checked against
// the JSON number grammar rather than strconv, which would also accept
// NaN/Inf and hex floats that later fail to marshal into the decision input;
// a value that fails is an error regardless of spec.IsRequired, since the
// value is present but unusable. json.Number keeps the raw digits instead of
// rounding through float64.
func resolveInputHeaderValue(
	field string, spec config.AuthzInputHeaderField, h http.Header,
) (value any, ok bool, err error) {
	raw := h.Get(spec.Header)
	switch spec.Type {
	case config.AuthzInputFieldTypeList:
		list := splitGroups(raw)
		if len(list) == 0 {
			return nil, false, nil
		}
		return list, true, nil
	case config.AuthzInputFieldTypeNumber:
		if raw == "" {
			return nil, false, nil
		}
		if _, marshalErr := json.Marshal(json.Number(raw)); marshalErr != nil {
			return nil, false, fmt.Errorf(
				"%w: field %q from header %q is not a number", ErrInvalidInputHeader,
				field, spec.Header,
			)
		}
		return json.Number(raw), true, nil
	default:
		if raw == "" {
			return nil, false, nil
		}
		return raw, true, nil
	}
}

// resolveExtra resolves every fromHeaders field against h. A required field
// with no usable value fails closed; an optional one is left out of the
// result entirely rather than emitted as an empty value, so the map stays
// nil when nothing resolves.
func resolveExtra(
	h http.Header, fromHeaders map[string]config.AuthzInputHeaderField,
) (map[string]any, error) {
	var extra map[string]any
	for field, spec := range fromHeaders {
		value, ok, err := resolveInputHeaderValue(field, spec, h)
		if err != nil {
			return nil, err
		}
		if !ok {
			if spec.IsRequired() {
				return nil, fmt.Errorf(
					"%w: field %q from header %q", ErrMissingInputHeader, field, spec.Header,
				)
			}
			continue
		}
		if extra == nil {
			extra = make(map[string]any, len(fromHeaders))
		}
		extra[field] = value
	}
	return extra, nil
}

// BypassRequested reports whether h carries cfg.Bypass set to the literal
// string "true" — an exact, case-sensitive match, so anything else (missing,
// empty, "True", "1") leaves authz enforcement in effect.
func BypassRequested(h http.Header, cfg config.AuthzHeaders) bool {
	return h.Get(cfg.Bypass) == "true"
}

// PrincipalFromHeader derives a Principal from a single occurrence of
// cfg.UserID and cfg.UserGroups in h (repeated headers are not merged — only
// the first value is read, per http.Header.Get). fromHeaders additionally
// resolves each configured field from h (config.AuthzInput.FromHeaders).
func PrincipalFromHeader(
	h http.Header, cfg config.AuthzHeaders, fromHeaders map[string]config.AuthzInputHeaderField,
) (Principal, error) {
	userID := h.Get(cfg.UserID)
	if userID == "" {
		return Principal{}, ErrMissingIdentity
	}
	groups := splitGroups(h.Get(cfg.UserGroups))
	if len(groups) == 0 {
		return Principal{}, ErrMissingIdentity
	}

	extra, err := resolveExtra(h, fromHeaders)
	if err != nil {
		return Principal{}, err
	}

	return Principal{UserID: userID, Groups: groups, Extra: extra}, nil
}
