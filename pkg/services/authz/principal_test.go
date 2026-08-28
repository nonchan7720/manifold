package authz

import (
	"encoding/json"
	"errors"
	"fmt"
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

	p, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Equal(t, []string{"team-finance", "team-ops", "team-ops"}, p.Groups)
}

func TestPrincipalFromHeader_MissingUserID_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-groups", "team-finance")

	_, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_EmptyUserID_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "")
	h.Set("x-user-groups", "team-finance")

	_, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_MissingGroups_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")

	_, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_EmptyGroups_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", "")

	_, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_OnlyCommasAndWhitespaceGroups_ErrMissingIdentity(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", " , , ")

	_, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.ErrorIs(t, err, ErrMissingIdentity)
}

func TestPrincipalFromHeader_RepeatedHeader_UsesFirstValueOnly(t *testing.T) {
	h := http.Header{}
	h.Add("x-user-id", "user-042")
	h.Add("x-user-id", "user-999")
	h.Add("x-user-groups", "team-finance")
	h.Add("x-user-groups", "team-ops")

	p, err := PrincipalFromHeader(h, defaultHeaders(), nil)
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Equal(t, []string{"team-finance"}, p.Groups)
}

func TestPrincipalFromHeader_CustomHeaderNames(t *testing.T) {
	h := http.Header{}
	h.Set("x-acme-user", "user-042")
	h.Set("x-acme-groups", "team-finance")

	cfg := config.AuthzHeaders{UserID: "x-acme-user", UserGroups: "x-acme-groups"}
	p, err := PrincipalFromHeader(h, cfg, nil)
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Equal(t, []string{"team-finance"}, p.Groups)
}

// --- PrincipalFromHeader: fromHeaders ---

func requiredField(header string) config.AuthzInputHeaderField {
	return config.AuthzInputHeaderField{Header: header}
}

func optionalField(header, typ string) config.AuthzInputHeaderField {
	required := false
	return config.AuthzInputHeaderField{Header: header, Required: &required, Type: typ}
}

func identityHeader() http.Header {
	h := http.Header{}
	h.Set("x-user-id", "user-042")
	h.Set("x-user-groups", "team-finance")
	return h
}

func TestPrincipalFromHeader_FromHeaders_Empty_Unaffected(t *testing.T) {
	p, err := PrincipalFromHeader(identityHeader(), defaultHeaders(), nil)
	require.NoError(t, err)
	require.Equal(t, "user-042", p.UserID)
	require.Empty(t, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_AllPresent_Resolved(t *testing.T) {
	h := identityHeader()
	h.Set("x-tenant-id", "acme")
	h.Set("x-region", "us-east")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"tenant": requiredField("x-tenant-id"),
		"region": requiredField("x-region"),
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"tenant": "acme", "region": "us-east"}, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_TypeStringIsTheDefault(t *testing.T) {
	h := identityHeader()
	h.Set("x-roles", " admin , auditor ")

	fromHeaders := map[string]config.AuthzInputHeaderField{"roles": requiredField("x-roles")}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"roles": " admin , auditor "}, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_TypeStringExplicit(t *testing.T) {
	h := identityHeader()
	h.Set("x-tenant-id", "acme")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"tenant": {Header: "x-tenant-id", Type: "string"},
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"tenant": "acme"}, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_TypeList_SplitsTrimsAndDropsEmpty(t *testing.T) {
	h := identityHeader()
	h.Set("x-roles", "admin, auditor ,,admin")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"roles": {Header: "x-roles", Type: "list"},
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"roles": []string{"admin", "auditor", "admin"}}, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_TypeNumber_KeepsRawPrecision(t *testing.T) {
	h := identityHeader()
	h.Set("x-seat-count", "12345678901234567890")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"seat_count": {Header: "x-seat-count", Type: "number"},
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"seat_count": json.Number("12345678901234567890")}, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_TypeNumber_Unparseable_ErrInvalidInputHeader(
	t *testing.T,
) {
	h := identityHeader()
	h.Set("x-seat-count", "many")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"seat_count": {Header: "x-seat-count", Type: "number"},
	}
	_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.ErrorIs(t, err, ErrInvalidInputHeader)
	require.NotErrorIs(t, err, ErrMissingIdentity)
	require.NotErrorIs(t, err, ErrMissingInputHeader)
	require.Contains(t, err.Error(), "seat_count")
	require.Contains(t, err.Error(), "x-seat-count")
}

func TestPrincipalFromHeader_FromHeaders_TypeNumber_UnparseableOptional_StillDenies(t *testing.T) {
	h := identityHeader()
	h.Set("x-seat-count", "many")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"seat_count": optionalField("x-seat-count", "number"),
	}
	_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.ErrorIs(t, err, ErrInvalidInputHeader)
}

func TestPrincipalFromHeader_FromHeaders_TypeNumber_NonJSONNumber_ErrInvalidInputHeader(
	t *testing.T,
) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "NaN", raw: "NaN"},
		{name: "lowercase nan", raw: "nan"},
		{name: "Inf", raw: "Inf"},
		{name: "signed Infinity", raw: "-Infinity"},
		{name: "leading plus", raw: "+1"},
		{name: "hexadecimal float", raw: "0x1p-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := identityHeader()
			h.Set("x-seat-count", tt.raw)

			fromHeaders := map[string]config.AuthzInputHeaderField{
				"seat_count": {Header: "x-seat-count", Type: "number"},
			}
			_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
			require.ErrorIs(t, err, ErrInvalidInputHeader)
			require.Contains(t, err.Error(), "seat_count")
			require.Contains(t, err.Error(), "x-seat-count")
		})
	}
}

func TestPrincipalFromHeader_FromHeaders_TypeNumber_JSONNumbersAreAccepted(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{name: "integer", raw: "42"},
		{name: "negative fraction", raw: "-3.5"},
		{name: "exponent", raw: "6.02e23"},
		{name: "integer beyond float64 precision", raw: "12345678901234567890"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := identityHeader()
			h.Set("x-seat-count", tt.raw)

			fromHeaders := map[string]config.AuthzInputHeaderField{
				"seat_count": {Header: "x-seat-count", Type: "number"},
			}
			p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"seat_count": json.Number(tt.raw)}, p.Extra)

			encoded, err := json.Marshal(p.Extra)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(`{"seat_count":%s}`, tt.raw), string(encoded))
		})
	}
}

func TestPrincipalFromHeader_FromHeaders_OneMissing_ErrMissingInputHeader(t *testing.T) {
	h := identityHeader()
	h.Set("x-tenant-id", "acme")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"tenant": requiredField("x-tenant-id"),
		"region": requiredField("x-region"),
	}
	_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.ErrorIs(t, err, ErrMissingInputHeader)
	require.NotErrorIs(t, err, ErrMissingIdentity)
	require.Contains(t, err.Error(), "region")
	require.Contains(t, err.Error(), "x-region")
}

func TestPrincipalFromHeader_FromHeaders_OneEmpty_ErrMissingInputHeader(t *testing.T) {
	h := identityHeader()
	h.Set("x-tenant-id", "acme")
	h.Set("x-region", "")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"tenant": requiredField("x-tenant-id"),
		"region": requiredField("x-region"),
	}
	_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.ErrorIs(t, err, ErrMissingInputHeader)
}

func TestPrincipalFromHeader_FromHeaders_TypeList_AllElementsEmpty_ErrMissingInputHeader(
	t *testing.T,
) {
	h := identityHeader()
	h.Set("x-roles", " , , ")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"roles": {Header: "x-roles", Type: "list"},
	}
	_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.ErrorIs(t, err, ErrMissingInputHeader)
}

func TestPrincipalFromHeader_FromHeaders_MissingIdentityWins(t *testing.T) {
	h := http.Header{}
	h.Set("x-user-groups", "team-finance")

	fromHeaders := map[string]config.AuthzInputHeaderField{"tenant": requiredField("x-tenant-id")}
	_, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.ErrorIs(t, err, ErrMissingIdentity)
	require.NotErrorIs(t, err, ErrMissingInputHeader)
}

// --- PrincipalFromHeader: fromHeaders, required: false ---

func TestPrincipalFromHeader_FromHeaders_OptionalMissing_OmittedFromExtra(t *testing.T) {
	h := identityHeader()
	h.Set("x-tenant-id", "acme")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"tenant": requiredField("x-tenant-id"),
		"region": optionalField("x-region", ""),
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"tenant": "acme"}, p.Extra)
	require.NotContains(t, p.Extra, "region")
}

func TestPrincipalFromHeader_FromHeaders_OptionalEmpty_OmittedFromExtra(t *testing.T) {
	h := identityHeader()
	h.Set("x-region", "")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"region": optionalField("x-region", ""),
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Empty(t, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_OptionalListWithNoUsableElement_OmittedFromExtra(
	t *testing.T,
) {
	h := identityHeader()
	h.Set("x-roles", " , , ")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"roles": optionalField("x-roles", "list"),
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Empty(t, p.Extra)
}

func TestPrincipalFromHeader_FromHeaders_OptionalPresent_Resolved(t *testing.T) {
	h := identityHeader()
	h.Set("x-seat-count", "42")

	fromHeaders := map[string]config.AuthzInputHeaderField{
		"seat_count": optionalField("x-seat-count", "number"),
	}
	p, err := PrincipalFromHeader(h, defaultHeaders(), fromHeaders)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"seat_count": json.Number("42")}, p.Extra)
}

// --- DenyReason ---

func TestDenyReason_ClassifiesEachSentinel(t *testing.T) {
	require.Equal(t, DenyReasonMissingIdentity, DenyReason(ErrMissingIdentity))
	require.Equal(t, DenyReasonMissingInputHeader,
		DenyReason(fmt.Errorf("%w: field", ErrMissingInputHeader)))
	require.Equal(t, DenyReasonInvalidInputHeader,
		DenyReason(fmt.Errorf("%w: field", ErrInvalidInputHeader)))
	require.Equal(t, DenyReasonPrincipalError, DenyReason(errors.New("boom")))
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
