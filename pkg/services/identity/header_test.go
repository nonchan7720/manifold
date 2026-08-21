package identity

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func newHeaderRequest(t *testing.T, header, value string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp/app1", nil)
	if value != "" {
		req.Header.Set(header, value)
	}
	return req
}

func TestHeaderResolver_Resolve_ReturnsIdentityKey(t *testing.T) {
	r, err := NewResolver(t.Context(), "sharedKeyUser", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-User-Id",
	}, nil)
	require.NoError(t, err)

	key, err := r.Resolve(t.Context(), newHeaderRequest(t, "X-User-Id", "user-a"))
	require.NoError(t, err)
	require.Equal(t, encodeIdentityKey("sharedKeyUser", "user-a"), key)
}

func TestHeaderResolver_Resolve_MissingHeader_ErrUnauthenticated(t *testing.T) {
	r, err := NewResolver(t.Context(), "sharedKeyUser", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-User-Id",
	}, nil)
	require.NoError(t, err)

	_, err = r.Resolve(t.Context(), newHeaderRequest(t, "X-User-Id", ""))
	require.ErrorIs(t, err, ErrUnauthenticated)
}

func TestHeaderResolver_Resolve_SameValue_StableKey(t *testing.T) {
	r, err := NewResolver(t.Context(), "sharedKeyUser", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-User-Id",
	}, nil)
	require.NoError(t, err)

	key1, err := r.Resolve(t.Context(), newHeaderRequest(t, "X-User-Id", "user-a"))
	require.NoError(t, err)
	key2, err := r.Resolve(t.Context(), newHeaderRequest(t, "X-User-Id", "user-a"))
	require.NoError(t, err)
	require.Equal(t, key1, key2)
}

// --- hash: true ---

func TestHeaderResolver_Resolve_Hash_DoesNotExposeRawValue(t *testing.T) {
	encryptKey := make([]byte, 32)
	r, err := NewResolver(t.Context(), "personalKey", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, encryptKey)
	require.NoError(t, err)

	key, err := r.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	require.NotContains(t, string(key), "raw-secret-value")
}

func TestHeaderResolver_Resolve_Hash_SameValue_StableKey(t *testing.T) {
	encryptKey := make([]byte, 32)
	r, err := NewResolver(t.Context(), "personalKey", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, encryptKey)
	require.NoError(t, err)

	key1, err := r.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	key2, err := r.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	require.Equal(t, key1, key2)
}

func TestHeaderResolver_Resolve_Hash_DifferentEncryptKey_DifferentIdentityKey(t *testing.T) {
	encryptKeyA := make([]byte, 32)
	encryptKeyB := make([]byte, 32)
	encryptKeyB[0] = 0x01

	rA, err := NewResolver(t.Context(), "personalKey", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, encryptKeyA)
	require.NoError(t, err)
	rB, err := NewResolver(t.Context(), "personalKey", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, encryptKeyB)
	require.NoError(t, err)

	keyA, err := rA.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	keyB, err := rB.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	require.NotEqual(t, keyA, keyB)
}

func TestHeaderResolver_NewResolver_Hash_NilEncryptKey_Error(t *testing.T) {
	_, err := NewResolver(t.Context(), "personalKey", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, nil)
	require.Error(t, err)
}

func TestHeaderResolver_NewResolver_Hash_WrongLengthEncryptKey_Error(t *testing.T) {
	_, err := NewResolver(t.Context(), "personalKey", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, make([]byte, 16))
	require.Error(t, err)
}

func TestHeaderResolver_Resolve_Hash_DifferentProfile_DifferentIdentityKey(t *testing.T) {
	// 同じ encryptKey・同じ生ヘッダー値でも、プロファイル名が異なれば HKDF の info
	// ラベルが分かれるため導出される identityKey も異なる（プロファイル間の分離）。
	encryptKey := make([]byte, 32)
	rA, err := NewResolver(t.Context(), "personalKeyA", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, encryptKey)
	require.NoError(t, err)
	rB, err := NewResolver(t.Context(), "personalKeyB", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-Api-Key",
		Hash:   true,
	}, encryptKey)
	require.NoError(t, err)

	keyA, err := rA.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	keyB, err := rB.Resolve(t.Context(), newHeaderRequest(t, "X-Api-Key", "raw-secret-value"))
	require.NoError(t, err)
	require.NotEqual(t, keyA, keyB)
}
