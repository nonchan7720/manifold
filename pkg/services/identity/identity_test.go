package identity

import (
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	domainedge "github.com/nonchan7720/manifold/pkg/domain/edge"
	"github.com/stretchr/testify/require"
)

// --- encodeIdentityKey ---

func TestEncodeIdentityKey_SameInputs_SameKey(t *testing.T) {
	require.Equal(t, encodeIdentityKey("oauth", "user-a"), encodeIdentityKey("oauth", "user-a"))
}

func TestEncodeIdentityKey_DifferentValues_DifferentKeys(t *testing.T) {
	require.NotEqual(t, encodeIdentityKey("oauth", "user-a"), encodeIdentityKey("oauth", "user-b"))
}

func TestEncodeIdentityKey_DifferentProfiles_DifferentKeys(t *testing.T) {
	require.NotEqual(
		t, encodeIdentityKey("oauth", "user-a"), encodeIdentityKey("sharedKeyUser", "user-a"),
	)
}

func TestEncodeIdentityKey_NoDelimiterAmbiguity(t *testing.T) {
	// 素朴な "profile:value" 連結だと ("a", "b:c") と ("a:b", "c") が衝突するが、
	// base64 で各要素を個別にエンコードしているため衝突しない。
	require.NotEqual(t, encodeIdentityKey("a", "b:c"), encodeIdentityKey("a:b", "c"))
}

func TestEncodeIdentityKey_NeverEqualsStaticIdentityKey(t *testing.T) {
	require.NotEqual(t, domainedge.StaticIdentityKey, encodeIdentityKey("static", ""))
	require.NotEqual(t, domainedge.StaticIdentityKey, encodeIdentityKey("", "static"))
}

// --- NewResolver: source 別ディスパッチ ---

func TestNewResolver_UnknownSource_Error(t *testing.T) {
	_, err := NewResolver(t.Context(), "bogus", &config.IdentityProfile{
		Source: config.IdentitySource("unknown"),
	}, nil)
	require.Error(t, err)
}

func TestNewResolver_Header_ReturnsResolver(t *testing.T) {
	r, err := NewResolver(t.Context(), "sharedKeyUser", &config.IdentityProfile{
		Source: config.IdentitySourceHeader,
		Header: "X-User-Id",
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, r)
}

func TestNewResolvers_BuildsOneResolverPerProfile(t *testing.T) {
	resolvers, err := NewResolvers(t.Context(), map[string]*config.IdentityProfile{
		"sharedKeyUser": {Source: config.IdentitySourceHeader, Header: "X-User-Id"},
		"personalKey":   {Source: config.IdentitySourceHeader, Header: "X-Api-Key", Hash: true},
	}, make([]byte, 32))
	require.NoError(t, err)
	require.Len(t, resolvers, 2)
	require.Contains(t, resolvers, "sharedKeyUser")
	require.Contains(t, resolvers, "personalKey")
}

func TestNewResolvers_PropagatesPerProfileError(t *testing.T) {
	_, err := NewResolvers(t.Context(), map[string]*config.IdentityProfile{
		"broken": {Source: config.IdentitySource("unknown")},
	}, nil)
	require.Error(t, err)
}
