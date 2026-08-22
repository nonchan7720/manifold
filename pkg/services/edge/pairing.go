package edge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"slices"
	"strconv"
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

	// pairIPRateLimitWindow/Max bound /edge/pair calls per source IP (see the
	// 持ち越し判断事項 #4 in docs/design/webmcp-reverse-gateway-phase2.ja.md).
	pairIPRateLimitWindow = time.Minute
	pairIPRateLimitMax    = 10
	ipRateLimitKeyPrefix  = "edge:pair_ratelimit:"

	// pairFailureLimit is how many ErrInvalidCode exchanges against the same
	// code value block it for the rest of its TTL window.
	pairFailureLimit     = 5
	pairFailureKeyPrefix = "edge:pair_fail:"
)

type pairingCodeData struct {
	IdentityKey string `json:"identityKey"`
}

// ipRateLimitData is RateLimitPairAttempt's fixed-window counter payload.
// ExpiresAt is fixed at the window's first request so incrementing the
// count on later requests (Set requires a TTL parameter) never slides the
// window forward.
type ipRateLimitData struct {
	Count     int       `json:"count"`
	ExpiresAt time.Time `json:"expiresAt"`
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

	// rateLimitMu and failureMu serialize RateLimitPairAttempt's and
	// recordExchangeFailure's own Get-modify-Set counters the same way as
	// appendMu, and carry the same v1 single-replica/sticky-LB caveat.
	// Separate locks since the two counters guard unrelated key spaces (IP
	// vs. pairing code) and serializing one must not block the other.
	rateLimitMu sync.Mutex
	failureMu   sync.Mutex
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

// RateLimitPairAttempt increments a fixed one-minute-window counter for ip
// (the caller of POST /edge/pair) and returns ErrIPRateLimited once more than
// pairIPRateLimitMax attempts have been seen within the window. This bounds
// brute-force volume independently of ExchangeCode's per-code failure
// counter (see 持ち越し判断事項 #4 in
// docs/design/webmcp-reverse-gateway-phase2.ja.md). An unexpected store
// failure resolving the current count is propagated rather than silently
// restarting the counter at 1.
func (s *PairingService) RateLimitPairAttempt(ctx context.Context, ip string) error {
	s.rateLimitMu.Lock()
	defer s.rateLimitMu.Unlock()

	key := ipRateLimitKeyPrefix + ip
	data := ipRateLimitData{Count: 1, ExpiresAt: time.Now().Add(pairIPRateLimitWindow)}
	ttl := pairIPRateLimitWindow

	stored, err := s.store.Get(ctx, key)
	switch {
	case err == nil:
		if err := json.Unmarshal([]byte(stored), &data); err != nil {
			return fmt.Errorf("edge: decode ip rate limit data: %w", err)
		}
		if data.Count >= pairIPRateLimitMax {
			return ErrIPRateLimited
		}
		data.Count++
		if ttl = time.Until(data.ExpiresAt); ttl <= 0 {
			ttl = pairIPRateLimitWindow
		}
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("edge: get ip rate limit: %w", err)
	}

	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := s.store.Set(ctx, key, payload, ttl); err != nil {
		return fmt.Errorf("edge: store ip rate limit: %w", err)
	}
	return nil
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
	if s.isPairCodeBlocked(ctx, code) {
		return "", ErrInvalidCode
	}

	key := pairingCodeKeyPrefix + code
	raw, err := s.store.Get(ctx, key)
	if err != nil {
		if recErr := s.recordExchangeFailure(ctx, code); recErr != nil {
			slog.ErrorContext(ctx, "edge: failed to record pairing code failure",
				slog.String("error", recErr.Error()))
		}
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

// isPairCodeBlocked reports whether code has already accumulated
// pairFailureLimit exchange failures, regardless of whether a pairing code
// currently exists under that value — a guess that later collides with a
// legitimately (re)issued code must still be rejected.
func (s *PairingService) isPairCodeBlocked(ctx context.Context, code string) bool {
	raw, err := s.store.Get(ctx, pairFailureKeyPrefix+code)
	if err != nil {
		return false
	}
	count, err := strconv.Atoi(raw)
	return err == nil && count >= pairFailureLimit
}

// recordExchangeFailure increments code's failure counter (TTL matches
// pairingCodeTTL, so the block expires alongside the code space it guards).
// Counting failures against a code that never existed is intentional and
// harmless — the counter key just expires with the TTL (持ち越し判断事項 #4).
// An unexpected store failure resolving the current count is propagated
// rather than silently restarting the counter at 1.
func (s *PairingService) recordExchangeFailure(ctx context.Context, code string) error {
	s.failureMu.Lock()
	defer s.failureMu.Unlock()

	key := pairFailureKeyPrefix + code
	count := 1
	stored, err := s.store.Get(ctx, key)
	switch {
	case err == nil:
		if n, convErr := strconv.Atoi(stored); convErr == nil {
			count = n + 1
		}
	case !errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("edge: get pairing code failure count: %w", err)
	}
	if err := s.store.Set(ctx, key, strconv.Itoa(count), pairingCodeTTL); err != nil {
		return fmt.Errorf("edge: record pairing code failure: %w", err)
	}
	if count >= pairFailureLimit {
		_ = s.store.Del(ctx, pairingCodeKeyPrefix+code)
	}
	return nil
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
