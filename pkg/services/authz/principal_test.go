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
