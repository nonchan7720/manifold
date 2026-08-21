package identity

import "errors"

var (
	// ErrUnauthenticated is returned when a request carries no usable
	// credential for the profile's source, or the credential fails
	// verification (bad signature, issuer/audience mismatch, expired token,
	// empty claim, inactive introspection result). Callers should respond
	// 401.
	ErrUnauthenticated = errors.New("identity: request is not authenticated")

	// ErrUnavailable is returned when a profile's identity source cannot be
	// reached (e.g. the introspection endpoint is down and no cached result
	// is available). Callers should respond 503.
	ErrUnavailable = errors.New("identity: identity source is temporarily unavailable")
)
