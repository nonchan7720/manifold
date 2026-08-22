package edge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/stretchr/testify/require"
)

func newTestPairingService(t *testing.T) *PairingService {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	return NewPairingService(storeClient)
}

// failingGetStore wraps a store.Client and fails Get for keys matching
// keyPrefix with a non-ErrNotFound error, simulating a backend outage
// (as opposed to a genuinely absent key) isolated to one key space.
type failingGetStore struct {
	store.Client
	keyPrefix string
	err       error
}

func (s *failingGetStore) Get(ctx context.Context, key string) (string, error) {
	if strings.HasPrefix(key, s.keyPrefix) {
		return "", s.err
	}
	return s.Client.Get(ctx, key)
}

// seedPairingCode stores a pairing code bound to identityKey directly,
// bypassing IssueCode's per-identityKey rate limit.
func seedPairingCode(t *testing.T, s *PairingService, identityKey domainedge.IdentityKey) string {
	t.Helper()
	code, err := generateNumericCode(pairingCodeDigits)
	require.NoError(t, err)
	raw, err := json.Marshal(pairingCodeData{IdentityKey: string(identityKey)})
	require.NoError(t, err)
	require.NoError(t, s.store.Set(t.Context(), pairingCodeKeyPrefix+code, raw, pairingCodeTTL))
	return code
}

func TestPairingService_IssueCode_ReturnsEightDigitCode(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)
	require.Len(t, code, 8)
	for _, r := range code {
		require.True(t, r >= '0' && r <= '9', "code must be all digits, got %q", code)
	}
}

func TestPairingService_ExchangeCode_ValidCode_ReturnsToken(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	token, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)
	require.NotEmpty(t, token)
}

func TestPairingService_ExchangeCode_UnknownCode_Error(t *testing.T) {
	s := newTestPairingService(t)
	_, err := s.ExchangeCode(t.Context(), "00000000", "")
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestPairingService_ExchangeCode_SingleUse(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	_, err = s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)

	_, err = s.ExchangeCode(t.Context(), code, "")
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestPairingService_ExchangeCode_WithValidExistingToken_AppendsBinding(t *testing.T) {
	s := newTestPairingService(t)
	firstCode, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)
	token, err := s.ExchangeCode(t.Context(), firstCode, "")
	require.NoError(t, err)

	secondCode, err := s.IssueCode(t.Context(), domainedge.IdentityKey("saml:user-a"))
	require.NoError(t, err)
	returnedToken, err := s.ExchangeCode(t.Context(), secondCode, token)
	require.NoError(t, err)
	require.Equal(t, token, returnedToken, "appending a binding must reuse the same edge token")

	keys, err := s.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.ElementsMatch(
		t,
		[]domainedge.IdentityKey{"oauth:user-a", "saml:user-a"},
		keys,
	)
}

func TestPairingService_ExchangeCode_WithValidExistingToken_SameIdentityIsIdempotent(t *testing.T) {
	s := newTestPairingService(t)
	firstCode, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)
	token, err := s.ExchangeCode(t.Context(), firstCode, "")
	require.NoError(t, err)

	// IssueCode rate-limits repeat codes for the same identityKey, which is
	// irrelevant to what this test exercises (ExchangeCode's dedup), so the
	// second code is seeded directly rather than through IssueCode.
	secondCode := seedPairingCode(t, s, domainedge.IdentityKey("oauth:user-a"))
	_, err = s.ExchangeCode(t.Context(), secondCode, token)
	require.NoError(t, err)

	keys, err := s.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.Equal(
		t,
		[]domainedge.IdentityKey{"oauth:user-a"},
		keys,
		"re-pairing the same identity must not duplicate the binding",
	)
}

func TestPairingService_ExchangeCode_WithInvalidExistingToken_IssuesNewToken(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)

	token, err := s.ExchangeCode(t.Context(), code, "bogus-existing-token")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	keys, err := s.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, []domainedge.IdentityKey{"oauth:user-a"}, keys)
}

func TestPairingService_ExchangeCode_ConcurrentAppendBinding_BothBindingsPersist(t *testing.T) {
	s := newTestPairingService(t)
	firstCode, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)
	token, err := s.ExchangeCode(t.Context(), firstCode, "")
	require.NoError(t, err)

	secondCode := seedPairingCode(t, s, domainedge.IdentityKey("saml:user-a"))
	thirdCode := seedPairingCode(t, s, domainedge.IdentityKey("oidc:user-a"))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, code := range []string{secondCode, thirdCode} {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			_, err := s.ExchangeCode(t.Context(), code, token)
			errs <- err
		}(code)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	keys, err := s.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.ElementsMatch(
		t,
		[]domainedge.IdentityKey{"oauth:user-a", "saml:user-a", "oidc:user-a"},
		keys,
		"both concurrent appends must persist, not just the last writer",
	)
}

func TestPairingService_Authenticate_DuringConcurrentAppendBinding_DoesNotDropBinding(
	t *testing.T,
) {
	s := newTestPairingService(t)
	firstCode, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)
	token, err := s.ExchangeCode(t.Context(), firstCode, "")
	require.NoError(t, err)

	secondCode := seedPairingCode(t, s, domainedge.IdentityKey("saml:user-a"))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = s.ExchangeCode(t.Context(), secondCode, token)
	}()
	go func() {
		defer wg.Done()
		for range 20 {
			_, _ = s.Authenticate(t.Context(), token)
		}
	}()
	wg.Wait()

	keys, err := s.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.ElementsMatch(
		t,
		[]domainedge.IdentityKey{"oauth:user-a", "saml:user-a"},
		keys,
		"Authenticate sliding the TTL must not overwrite a concurrent append",
	)
}

func TestPairingService_ExchangeCode_BindingsAreIsolatedBetweenTokens(t *testing.T) {
	s := newTestPairingService(t)
	codeA, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)
	tokenA, err := s.ExchangeCode(t.Context(), codeA, "")
	require.NoError(t, err)

	codeB, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-b"))
	require.NoError(t, err)
	tokenB, err := s.ExchangeCode(t.Context(), codeB, "")
	require.NoError(t, err)

	require.NotEqual(t, tokenA, tokenB)
	keysA, err := s.Authenticate(t.Context(), tokenA)
	require.NoError(t, err)
	require.Equal(t, []domainedge.IdentityKey{"oauth:user-a"}, keysA)

	keysB, err := s.Authenticate(t.Context(), tokenB)
	require.NoError(t, err)
	require.Equal(t, []domainedge.IdentityKey{"oauth:user-b"}, keysB)
}

func TestPairingService_IssueCode_RateLimited(t *testing.T) {
	s := newTestPairingService(t)
	_, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	_, err = s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.ErrorIs(t, err, ErrRateLimited)
}

func TestPairingService_IssueCode_RateLimitIsPerIdentity(t *testing.T) {
	s := newTestPairingService(t)
	_, err := s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-a"))
	require.NoError(t, err)

	_, err = s.IssueCode(t.Context(), domainedge.IdentityKey("oauth:user-b"))
	require.NoError(t, err, "rate limit must not leak across identities")
}

func TestPairingService_Authenticate_ValidToken_ReturnsIdentityKeys(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)
	token, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)

	keys, err := s.Authenticate(t.Context(), token)
	require.NoError(t, err)
	require.Equal(t, []domainedge.IdentityKey{domainedge.StaticIdentityKey}, keys)
}

func TestPairingService_Authenticate_UnknownToken_Error(t *testing.T) {
	s := newTestPairingService(t)
	_, err := s.Authenticate(t.Context(), "bogus-token")
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestPairingService_Authenticate_RevokedToken_Error(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)
	token, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)

	require.NoError(t, s.Revoke(t.Context(), token))

	_, err = s.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, ErrInvalidToken)
}

func TestPairingService_Authenticate_TokenWithNoBindings_ErrNotPaired(t *testing.T) {
	s := newTestPairingService(t)
	const token = "zero-binding-token"
	raw, err := json.Marshal(edgeTokenData{IdentityKeys: []string{}})
	require.NoError(t, err)
	require.NoError(t, s.store.Set(t.Context(), edgeTokenKeyPrefix+token, raw, edgeTokenSlidingTTL))

	_, err = s.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, ErrNotPaired)
}

// --- RateLimitPairAttempt: IP 単位の固定窓レート制限（/edge/pair 総当たり対策） ---

func TestPairingService_RateLimitPairAttempt_AllowsUpToTenWithinWindow(t *testing.T) {
	s := newTestPairingService(t)
	for i := range 10 {
		require.NoError(t, s.RateLimitPairAttempt(t.Context(), "203.0.113.1"), "attempt %d", i+1)
	}
}

func TestPairingService_RateLimitPairAttempt_EleventhWithinWindow_RateLimited(t *testing.T) {
	s := newTestPairingService(t)
	for range 10 {
		require.NoError(t, s.RateLimitPairAttempt(t.Context(), "203.0.113.1"))
	}

	err := s.RateLimitPairAttempt(t.Context(), "203.0.113.1")
	require.ErrorIs(t, err, ErrIPRateLimited)
}

func TestPairingService_RateLimitPairAttempt_IndependentPerIP(t *testing.T) {
	s := newTestPairingService(t)
	for range 10 {
		require.NoError(t, s.RateLimitPairAttempt(t.Context(), "203.0.113.1"))
	}

	require.NoError(
		t,
		s.RateLimitPairAttempt(t.Context(), "203.0.113.2"),
		"rate limit must not leak across IPs",
	)
}

func TestPairingService_RateLimitPairAttempt_UnexpectedStoreError_Propagates(t *testing.T) {
	memClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = memClient.Close() })
	wantErr := errors.New("store unavailable")
	failing := &failingGetStore{Client: memClient, keyPrefix: ipRateLimitKeyPrefix, err: wantErr}
	s := NewPairingService(failing)

	err = s.RateLimitPairAttempt(t.Context(), "203.0.113.1")
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrIPRateLimited),
		"an unexpected store failure resolving the counter must not look like "+
			"a normal rate limit hit")
}

func TestPairingService_RateLimitPairAttempt_ConcurrentCallsCountExactly(t *testing.T) {
	s := newTestPairingService(t)
	const attempts = 30

	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	for i := range attempts {
		wg.Go(func() {
			start.Wait()
			errs[i] = s.RateLimitPairAttempt(t.Context(), "203.0.113.99")
		})
	}
	start.Done()
	wg.Wait()

	var allowed, limited int
	for _, err := range errs {
		switch {
		case err == nil:
			allowed++
		case errors.Is(err, ErrIPRateLimited):
			limited++
		default:
			require.NoError(t, err)
		}
	}
	require.Equal(t, pairIPRateLimitMax, allowed,
		"a non-atomic Get-modify-Set undercounts concurrent attempts, letting more than "+
			"pairIPRateLimitMax through")
	require.Equal(t, attempts-pairIPRateLimitMax, limited)
}

// --- ExchangeCode: 失敗 N 回でコード無効化（/edge/pair 総当たり対策） ---

// seedPairingCodeWithValue seeds a pairing code for an explicit code value
// (rather than one generated by generateNumericCode), so a test can force a
// collision between an attacker's earlier guesses and a later legitimately
// issued code.
func seedPairingCodeWithValue(
	t *testing.T,
	s *PairingService,
	code string,
	identityKey domainedge.IdentityKey,
) {
	t.Helper()
	raw, err := json.Marshal(pairingCodeData{IdentityKey: string(identityKey)})
	require.NoError(t, err)
	require.NoError(t, s.store.Set(t.Context(), pairingCodeKeyPrefix+code, raw, pairingCodeTTL))
}

func TestPairingService_ExchangeCode_FiveFailures_BlocksSubsequentValidCode(t *testing.T) {
	s := newTestPairingService(t)
	const code = "00000042"

	for i := range 5 {
		_, err := s.ExchangeCode(t.Context(), code, "")
		require.ErrorIs(t, err, ErrInvalidCode, "failure %d", i+1)
	}

	// Even if this exact code value later becomes a legitimately issued
	// pairing code, exchange must still fail: 5 failures already blocked it
	// for the remainder of its TTL window.
	seedPairingCodeWithValue(t, s, code, domainedge.StaticIdentityKey)

	_, err := s.ExchangeCode(t.Context(), code, "")
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestPairingService_ExchangeCode_FourFailures_DoesNotBlockValidCode(t *testing.T) {
	s := newTestPairingService(t)
	const code = "00000043"

	for range 4 {
		_, err := s.ExchangeCode(t.Context(), code, "")
		require.ErrorIs(t, err, ErrInvalidCode)
	}

	seedPairingCodeWithValue(t, s, code, domainedge.StaticIdentityKey)

	_, err := s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err, "fewer than the failure limit must not block a later valid code")
}

func TestPairingService_ExchangeCode_ConcurrentFailures_BlocksAfterExactlyFiveFailures(
	t *testing.T,
) {
	s := newTestPairingService(t)
	const code = "00000099"
	const attempts = 20

	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for range attempts {
		wg.Go(func() {
			start.Wait()
			_, _ = s.ExchangeCode(t.Context(), code, "")
		})
	}
	start.Done()
	wg.Wait()

	// Even if this code value later becomes legitimately issued, it must
	// stay blocked: a non-atomic failure counter could undercount concurrent
	// failures below pairFailureLimit.
	seedPairingCodeWithValue(t, s, code, domainedge.StaticIdentityKey)
	_, err := s.ExchangeCode(t.Context(), code, "")
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestPairingService_recordExchangeFailure_UnexpectedStoreError_Propagates(t *testing.T) {
	memClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = memClient.Close() })
	wantErr := errors.New("store unavailable")
	failing := &failingGetStore{Client: memClient, keyPrefix: pairFailureKeyPrefix, err: wantErr}
	s := NewPairingService(failing)

	err = s.recordExchangeFailure(t.Context(), "00000000")
	require.Error(t, err)
}

func TestPairingService_ExchangeCode_RecordFailureStoreError_DoesNotWriteFailureCounter(
	t *testing.T,
) {
	memClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = memClient.Close() })
	failing := &failingGetStore{
		Client:    memClient,
		keyPrefix: pairFailureKeyPrefix,
		err:       errors.New("store unavailable"),
	}
	s := NewPairingService(failing)

	_, err = s.ExchangeCode(t.Context(), "00000000", "")
	require.ErrorIs(t, err, ErrInvalidCode,
		"the code itself is invalid regardless of the failure-counter store's health")

	_, getErr := memClient.Get(t.Context(), pairFailureKeyPrefix+"00000000")
	require.ErrorIs(t, getErr, store.ErrNotFound,
		"recordExchangeFailure must not silently write a fresh counter over an "+
			"unexpected store failure")
}

func TestPairingService_ExchangeCode_Success_DoesNotCreateFailureCounter(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	_, err = s.ExchangeCode(t.Context(), code, "")
	require.NoError(t, err)

	_, err = s.store.Get(t.Context(), pairFailureKeyPrefix+code)
	require.Error(t, err, "a successful exchange must not create a failure counter entry")
}
