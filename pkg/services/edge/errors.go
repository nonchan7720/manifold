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
)
