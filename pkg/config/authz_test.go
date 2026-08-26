package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- AuthzConfig.WithDefaults ---

func TestAuthzConfig_WithDefaults_FillsAllWhenEmpty(t *testing.T) {
	got := AuthzConfig{}.WithDefaults()
	require.Equal(t, DefaultAuthzOPAURL, got.OPAURL)
	require.Equal(t, DefaultAuthzTimeout, got.Timeout)
	require.Equal(t, DefaultAuthzDecisionPathList, got.DecisionPath.List)
	require.Equal(t, DefaultAuthzDecisionPathCall, got.DecisionPath.Call)
	require.Equal(t, DefaultAuthzHeaderUserID, got.Headers.UserID)
	require.Equal(t, DefaultAuthzHeaderUserGroups, got.Headers.UserGroups)
}

func TestAuthzConfig_WithDefaults_AdminGroupsEmptyByDefault(t *testing.T) {
	got := AuthzConfig{}.WithDefaults()
	require.Empty(t, got.AdminGroups)
}

func TestAuthzConfig_WithDefaults_KeepsAdminGroups(t *testing.T) {
	got := AuthzConfig{AdminGroups: []string{"team-platform"}}.WithDefaults()
	require.Equal(t, []string{"team-platform"}, got.AdminGroups)
}

func TestAuthzConfig_WithDefaults_FillsTimeoutWhenZero(t *testing.T) {
	got := AuthzConfig{Timeout: 0}.WithDefaults()
	require.Equal(t, DefaultAuthzTimeout, got.Timeout)
}

func TestAuthzConfig_WithDefaults_KeepsNegativeTimeout(t *testing.T) {
	// 負値は補完せずそのまま残す。起動時に validatePositiveDuration がエラーにする。
	got := AuthzConfig{Timeout: -1 * time.Second}.WithDefaults()
	require.Equal(t, -1*time.Second, got.Timeout)
}

func TestAuthzConfig_WithDefaults_KeepsExplicitValues(t *testing.T) {
	got := AuthzConfig{
		Enabled: true,
		OPAURL:  "https://opa.internal.example.com:8181",
		Timeout: 10 * time.Second,
		DecisionPath: AuthzDecisionPath{
			List: "/v1/data/acme/authz/tools",
			Call: "/v1/data/acme/authz/call",
		},
		Headers: AuthzHeaders{
			UserID:     "x-acme-user",
			UserGroups: "x-acme-groups",
		},
	}.WithDefaults()
	require.Equal(t, "https://opa.internal.example.com:8181", got.OPAURL)
	require.Equal(t, 10*time.Second, got.Timeout)
	require.Equal(t, "/v1/data/acme/authz/tools", got.DecisionPath.List)
	require.Equal(t, "/v1/data/acme/authz/call", got.DecisionPath.Call)
	require.Equal(t, "x-acme-user", got.Headers.UserID)
	require.Equal(t, "x-acme-groups", got.Headers.UserGroups)
}

// --- AuthzConfig.ValidateWithContext: enabled=false ---

func TestAuthzConfig_ValidateWithContext_Disabled_Valid(t *testing.T) {
	c := AuthzConfig{}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Disabled_IgnoresInvalidValues(t *testing.T) {
	c := AuthzConfig{
		Enabled: false,
		OPAURL:  "ftp://opa.internal.example.com",
		Timeout: -1 * time.Second,
		DecisionPath: AuthzDecisionPath{
			List: "no-leading-slash",
			Call: "no-leading-slash",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

// --- AuthzConfig.ValidateWithContext: enabled=true ---

func TestAuthzConfig_ValidateWithContext_Enabled_DefaultsValid(t *testing.T) {
	c := AuthzConfig{Enabled: true}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_AcceptsExplicitValidValues(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		OPAURL:  "https://opa.internal.example.com:8181",
		Timeout: 5 * time.Second,
		DecisionPath: AuthzDecisionPath{
			List: "/v1/data/acme/authz/tools",
			Call: "/v1/data/acme/authz/call",
		},
		Headers: AuthzHeaders{
			UserID:     "x-acme-user",
			UserGroups: "x-acme-groups",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsNonHTTPScheme(t *testing.T) {
	c := AuthzConfig{Enabled: true, OPAURL: "ftp://opa.internal.example.com"}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "OPAURL")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsMalformedOPAURL(t *testing.T) {
	c := AuthzConfig{Enabled: true, OPAURL: "opa.internal.example.com"}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsDecisionPathListWithoutLeadingSlash(
	t *testing.T,
) {
	c := AuthzConfig{
		Enabled:      true,
		DecisionPath: AuthzDecisionPath{List: "v1/data/acme/authz/tools"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "List")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsDecisionPathCallWithoutLeadingSlash(
	t *testing.T,
) {
	c := AuthzConfig{
		Enabled:      true,
		DecisionPath: AuthzDecisionPath{Call: "v1/data/acme/authz/call"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Call")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsNegativeTimeout(t *testing.T) {
	c := AuthzConfig{Enabled: true, Timeout: -1 * time.Second}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Timeout")
}

func TestAuthzConfig_ValidateWithContext_Enabled_ZeroTimeout_UsesDefault(t *testing.T) {
	c := AuthzConfig{Enabled: true, Timeout: 0}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

// --- AuthzConfig.ValidateWithContext: enabled=true, Headers ---

func TestAuthzConfig_ValidateWithContext_Enabled_DefaultHeaders_Valid(t *testing.T) {
	c := AuthzConfig{Enabled: true}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsUserIDHeaderWithSpace(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{UserID: "x acme user", UserGroups: "x-acme-groups"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "UserID")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsUserGroupsHeaderWithColon(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{UserID: "x-acme-user", UserGroups: "x-acme:groups"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "UserGroups")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsSameHeaderName(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{UserID: "x-acme-identity", UserGroups: "x-acme-identity"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsSameHeaderNameCaseInsensitive(
	t *testing.T,
) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{UserID: "X-Acme-Identity", UserGroups: "x-acme-identity"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
}
