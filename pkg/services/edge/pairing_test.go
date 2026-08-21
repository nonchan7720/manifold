package edge

import (
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

	token, err := s.ExchangeCode(t.Context(), code)
	require.NoError(t, err)
	require.NotEmpty(t, token)
}

func TestPairingService_ExchangeCode_UnknownCode_Error(t *testing.T) {
	s := newTestPairingService(t)
	_, err := s.ExchangeCode(t.Context(), "00000000")
	require.ErrorIs(t, err, ErrInvalidCode)
}

func TestPairingService_ExchangeCode_SingleUse(t *testing.T) {
	s := newTestPairingService(t)
	code, err := s.IssueCode(t.Context(), domainedge.StaticIdentityKey)
	require.NoError(t, err)

	_, err = s.ExchangeCode(t.Context(), code)
	require.NoError(t, err)

	_, err = s.ExchangeCode(t.Context(), code)
	require.ErrorIs(t, err, ErrInvalidCode)
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
	token, err := s.ExchangeCode(t.Context(), code)
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
	token, err := s.ExchangeCode(t.Context(), code)
	require.NoError(t, err)

	require.NoError(t, s.Revoke(t.Context(), token))

	_, err = s.Authenticate(t.Context(), token)
	require.ErrorIs(t, err, ErrInvalidToken)
}
