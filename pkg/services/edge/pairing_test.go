package edge

import (
	"encoding/json"
	"testing"

	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/nonchan7720/manifold/pkg/infrastructure/memory"
	"github.com/stretchr/testify/require"
)

func newTestPairingService(t *testing.T) *PairingService {
	t.Helper()
	storeClient, err := memory.NewClient(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = storeClient.Close() })
	return NewPairingService(storeClient)
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
