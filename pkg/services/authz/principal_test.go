package authz

import (
	"net/http"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

func defaultHeaders() config.AuthzHeaders {
	return config.AuthzHeaders{UserID: "x-user-id", UserGroups: "x-user-groups"}
}

func TestPrincipalFromHeader_SplitsTrimsAndDropsEmptyGroups(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", "team-finance, team-ops ,,team-ops")

	p, err := PrincipalFromHeader(h, defaultHeaders())
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Equal(t, []string{"team-finance", "team-ops", "team-ops"}, p.Groups)
}

func TestPrincipalFromHeader_MissingUserID_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-groups", "team-finance")

	_, err := PrincipalFromHeader(h, defaultHeaders())
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_EmptyUserID_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "")
	h.Set("x-user-groups", "team-finance")

	_, err := PrincipalFromHeader(h, defaultHeaders())
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_MissingGroups_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")

	_, err := PrincipalFromHeader(h, defaultHeaders())
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_EmptyGroups_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", "")

	_, err := PrincipalFromHeader(h, defaultHeaders())
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_OnlyCommasAndWhitespaceGroups_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", " , , ")

	_, err := PrincipalFromHeader(h, defaultHeaders())
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_RepeatedHeader_UsesFirstValueOnly(t *testing.T) {
	h := http.Header{}
	h.Add("x-user-id", "user-042")
	h.Add("x-user-id", "user-999")
	h.Add("x-user-groups", "team-finance")
	h.Add("x-user-groups", "team-ops")

	p, err := PrincipalFromHeader(h, defaultHeaders())
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Equal(t, []string{"team-finance"}, p.Groups)
}

func TestPrincipalFromHeader_CustomHeaderNames(t *testing.T) {
	h := http.Header{}
	h.Set("x-acme-user", "user-042")
	h.Set("x-acme-groups", "team-finance")

	cfg := config.AuthzHeaders{UserID: "x-acme-user", UserGroups: "x-acme-groups"}
	p, err := PrincipalFromHeader(h, cfg)
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Equal(t, []string{"team-finance"}, p.Groups)
}

// --- BypassRequested ---

func defaultHeadersWithBypass() config.AuthzHeaders {
	return config.AuthzHeaders{
		UserID:     "x-user-id",
		UserGroups: "x-user-groups",
		Bypass:     "x-authz-bypass",
	}
}

func TestBypassRequested_ExactLowercaseTrue_ReturnsTrue(t *testing.T) {
	h := http.Header{}
	h.Set("x-authz-bypass", "true")

	require.True(t, BypassRequested(h, defaultHeadersWithBypass()))
}

func TestBypassRequested_Missing_ReturnsFalse(t *testing.T) {
	h := http.Header{}

	require.False(t, BypassRequested(h, defaultHeadersWithBypass()))
}

func TestBypassRequested_Empty_ReturnsFalse(t *testing.T) {
	h := http.Header{}
	h.Set("x-authz-bypass", "")

	require.False(t, BypassRequested(h, defaultHeadersWithBypass()))
}

func TestBypassRequested_CapitalizedTrue_ReturnsFalse(t *testing.T) {
	h := http.Header{}
	h.Set("x-authz-bypass", "True")

	require.False(t, BypassRequested(h, defaultHeadersWithBypass()))
}

func TestBypassRequested_NumericOne_ReturnsFalse(t *testing.T) {
	h := http.Header{}
	h.Set("x-authz-bypass", "1")

	require.False(t, BypassRequested(h, defaultHeadersWithBypass()))
}

func TestBypassRequested_TrueWithTrailingSpace_ReturnsFalse(t *testing.T) {
	h := http.Header{}
	h.Set("x-authz-bypass", "true ")

	require.False(t, BypassRequested(h, defaultHeadersWithBypass()))
}

func TestBypassRequested_CustomHeaderName(t *testing.T) {
	h := http.Header{}
	h.Set("x-acme-bypass", "true")

	cfg := config.AuthzHeaders{
		UserID:     "x-acme-user",
		UserGroups: "x-acme-groups",
		Bypass:     "x-acme-bypass",
	}
	require.True(t, BypassRequested(h, cfg))
}
