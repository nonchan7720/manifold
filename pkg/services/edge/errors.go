package edge

import "errors"

var (
	// ErrInvalidCode is returned when a pairing code is unknown, expired, or
	// already consumed.
	ErrInvalidCode = errors.New("edge: invalid or expired pairing code")

	// ErrInvalidToken is returned when an edge token is unknown, expired, or
	// revoked.
	ErrInvalidToken = errors.New("edge: invalid or expired edge token")

	// ErrRateLimited is returned when a pairing code is requested again for
	// the same identityKey within the rate-limit window.
	ErrRateLimited = errors.New("edge: pairing code requested too soon, try again shortly")

	// ErrNotPaired is returned when an edge token carries no identityKey
	// binding at all. Callers should surface the create_pairing_code tool as
	// the way to resolve it.
	ErrNotPaired = errors.New(
		"edge: token is not paired with any identity; call create_pairing_code and " +
			"pair the browser extension with the issued code",
	)
)
