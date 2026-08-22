package edge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
)

const (
	pairingCodeDigits    = 8
	pairingCodeTTL       = 5 * time.Minute
	pairingRateLimitTTL  = 30 * time.Second
	edgeTokenSlidingTTL  = 30 * 24 * time.Hour
	pairingCodeKeyPrefix = "edge:pairing_code:"
	rateLimitKeyPrefix   = "edge:pairing_ratelimit:"
	edgeTokenKeyPrefix   = "edge:token:"
)

type pairingCodeData struct {
	IdentityKey string `json:"identityKey"`
}

type edgeTokenData struct {
	IdentityKeys []string `json:"identityKeys"`
	Revoked      bool     `json:"revoked"`
}

// PairingService issues short-lived pairing codes and exchanges them for
// long-lived edge tokens (see the "pairing モード" section of
// docs/design/webmcp-reverse-gateway.ja.md).
type PairingService struct {
	store store.Client

	// appendMu serializes appendBinding's Get-modify-Set against the store,
	// since store.Client has no CAS/transaction to make that sequence atomic
	// on its own. Correct as long as this is the only PairingService writing
	// edge tokens for a deployment (v1: single replica or a sticky LB in
	// front of several); cross-replica atomicity needs store-side CAS and is
	// Phase 3.
	appendMu sync.Mutex
}

// NewPairingService creates a PairingService backed by storeClient.
func NewPairingService(storeClient store.Client) *PairingService {
	return &PairingService{store: storeClient}
}

// IssueCode generates an 8-digit, single-use, 5-minute pairing code bound to
// identityKey. Returns ErrRateLimited if called again for the same
// identityKey within the rate-limit window.
func (s *PairingService) IssueCode(
	ctx context.Context,
	identityKey domainedge.IdentityKey,
) (string, error) {
	rateLimitKey := rateLimitKeyPrefix + string(identityKey)
	if _, err := s.store.Get(ctx, rateLimitKey); err == nil {
		return "", ErrRateLimited
	}
	if err := s.store.Set(ctx, rateLimitKey, "1", pairingRateLimitTTL); err != nil {
		return "", fmt.Errorf("edge: set pairing rate limit guard: %w", err)
	}

	code, err := generateNumericCode(pairingCodeDigits)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(pairingCodeData{IdentityKey: string(identityKey)})
	if err != nil {
		return "", err
	}
	if err := s.store.Set(ctx, pairingCodeKeyPrefix+code, raw, pairingCodeTTL); err != nil {
		return "", fmt.Errorf("edge: store pairing code: %w", err)
	}
	return code, nil
}

// ExchangeCode validates and consumes a pairing code (single use), binding
// the code's identityKey to an edge token.
//
// If existingToken is non-empty and still valid (unrevoked, not expired),
// the identityKey is appended to that token's existing bindings (a no-op if
// already present) and the same token is returned, so a browser extension
// that already holds a token from another server sharing the profile needs
// no second pairing round. Any other existingToken value (empty, unknown,
// expired, or revoked) issues a brand-new token carrying only this
// identityKey, letting the extension self-heal by swapping its stored token.
func (s *PairingService) ExchangeCode(
	ctx context.Context,
	code string,
	existingToken string,
) (string, error) {
	key := pairingCodeKeyPrefix + code
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return "", ErrInvalidCode
	}
	_ = s.store.Del(ctx, key)

	var data pairingCodeData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return "", fmt.Errorf("edge: decode pairing code data: %w", err)
	}

	if existingToken != "" {
		token, err := s.appendBinding(ctx, existingToken, data.IdentityKey)
		switch {
		case err == nil:
			return token, nil
		case !errors.Is(err, errTokenNotAppendable):
			return "", err
		}
	}

	token := uuid.NewString()
	tokenRaw, err := json.Marshal(edgeTokenData{IdentityKeys: []string{data.IdentityKey}})
	if err != nil {
		return "", err
	}
	err = s.store.Set(ctx, edgeTokenKeyPrefix+token, tokenRaw, edgeTokenSlidingTTL)
	if err != nil {
		return "", fmt.Errorf("edge: store edge token: %w", err)
	}
	return token, nil
}

// errTokenNotAppendable is appendBinding's internal signal that
// existingToken is unusable (unknown, expired, or revoked); ExchangeCode
// treats it as "fall back to issuing a new token", not a hard failure.
var errTokenNotAppendable = errors.New("edge: existing token not usable for append")

// appendBinding adds identityKey to token's binding set (a no-op if already
// present) if token is valid.
func (s *PairingService) appendBinding(
	ctx context.Context,
	token string,
	identityKey string,
) (string, error) {
	s.appendMu.Lock()
	defer s.appendMu.Unlock()

	key := edgeTokenKeyPrefix + token
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return "", errTokenNotAppendable
	}
	var data edgeTokenData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return "", fmt.Errorf("edge: decode edge token data: %w", err)
	}
	if data.Revoked {
		return "", errTokenNotAppendable
	}

	if !slices.Contains(data.IdentityKeys, identityKey) {
		data.IdentityKeys = append(data.IdentityKeys, identityKey)
	}
	tokenRaw, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	if err := s.store.Set(ctx, key, tokenRaw, edgeTokenSlidingTTL); err != nil {
		return "", fmt.Errorf("edge: store edge token: %w", err)
	}
	return token, nil
}

// Authenticate validates an edge token, slides its TTL forward, and returns
// the identityKey(s) bound to it.
func (s *PairingService) Authenticate(
	ctx context.Context,
	token string,
) ([]domainedge.IdentityKey, error) {
	key := edgeTokenKeyPrefix + token
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		return nil, ErrInvalidToken
	}
	var data edgeTokenData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, fmt.Errorf("edge: decode edge token data: %w", err)
	}
	if data.Revoked {
		return nil, ErrInvalidToken
	}
	if len(data.IdentityKeys) == 0 {
		return nil, ErrNotPaired
	}
	// Expire (not Set) so this doesn't overwrite the payload with what was
	// read here, which would silently drop a binding appendBinding wrote
	// concurrently.
	if err := s.store.Expire(ctx, key, edgeTokenSlidingTTL); err != nil {
		return nil, fmt.Errorf("edge: slide edge token ttl: %w", err)
	}

	keys := make([]domainedge.IdentityKey, len(data.IdentityKeys))
	for i, k := range data.IdentityKeys {
		keys[i] = domainedge.IdentityKey(k)
	}
	return keys, nil
}

// Revoke invalidates an edge token immediately.
func (s *PairingService) Revoke(ctx context.Context, token string) error {
	return s.store.Del(ctx, edgeTokenKeyPrefix+token)
}

func generateNumericCode(digits int) (string, error) {
	upperBound := big.NewInt(1)
	ten := big.NewInt(10)
	for range digits {
		upperBound.Mul(upperBound, ten)
	}
	n, err := rand.Int(rand.Reader, upperBound)
	if err != nil {
		return "", fmt.Errorf("edge: generate pairing code: %w", err)
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}
