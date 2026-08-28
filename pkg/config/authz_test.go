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
	require.Equal(t, DefaultAuthzDecisionPathCatalog, got.DecisionPath.Catalog)
	require.Equal(t, DefaultAuthzHeaderUserID, got.Headers.UserID)
	require.Equal(t, DefaultAuthzHeaderUserGroups, got.Headers.UserGroups)
	require.Equal(t, DefaultAuthzHeaderBypass, got.Headers.Bypass)
	require.Equal(t, DefaultAuthzInputUser, got.Input.User)
	require.Equal(t, DefaultAuthzInputGroups, got.Input.Groups)
	require.Equal(t, DefaultAuthzInputServer, got.Input.Server)
	require.Equal(t, DefaultAuthzInputTool, got.Input.Tool)
	require.Equal(t, DefaultAuthzInputTools, got.Input.Tools)
	require.Equal(t, DefaultAuthzInputToolName, got.Input.ToolName)
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
			List:    "/v1/data/acme/authz/tools",
			Call:    "/v1/data/acme/authz/call",
			Catalog: "/v1/data/acme/authz/catalog",
		},
		Headers: AuthzHeaders{
			UserID:     "x-acme-user",
			UserGroups: "x-acme-groups",
			Bypass:     "x-acme-bypass",
		},
		Input: AuthzInput{
			User:     "acme-user",
			Groups:   "acme-groups",
			Server:   "acme-server",
			Tool:     "acme-tool",
			Tools:    "acme-tools",
			ToolName: "acme-tool-name",
		},
	}.WithDefaults()
	require.Equal(t, "https://opa.internal.example.com:8181", got.OPAURL)
	require.Equal(t, 10*time.Second, got.Timeout)
	require.Equal(t, "/v1/data/acme/authz/tools", got.DecisionPath.List)
	require.Equal(t, "/v1/data/acme/authz/call", got.DecisionPath.Call)
	require.Equal(t, "/v1/data/acme/authz/catalog", got.DecisionPath.Catalog)
	require.Equal(t, "x-acme-user", got.Headers.UserID)
	require.Equal(t, "x-acme-groups", got.Headers.UserGroups)
	require.Equal(t, "x-acme-bypass", got.Headers.Bypass)
	require.Equal(t, "acme-user", got.Input.User)
	require.Equal(t, "acme-groups", got.Input.Groups)
	require.Equal(t, "acme-server", got.Input.Server)
	require.Equal(t, "acme-tool", got.Input.Tool)
	require.Equal(t, "acme-tools", got.Input.Tools)
	require.Equal(t, "acme-tool-name", got.Input.ToolName)
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
			List:    "no-leading-slash",
			Call:    "no-leading-slash",
			Catalog: "no-leading-slash",
		},
		Input: AuthzInput{User: "", Groups: ""},
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
			List:    "/v1/data/acme/authz/tools",
			Call:    "/v1/data/acme/authz/call",
			Catalog: "/v1/data/acme/authz/catalog",
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

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsDecisionPathCatalogWithoutLeadingSlash(
	t *testing.T,
) {
	c := AuthzConfig{
		Enabled:      true,
		DecisionPath: AuthzDecisionPath{Catalog: "v1/data/acme/authz/catalog"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Catalog")
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

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsBypassHeaderWithSpace(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{
			UserID:     "x-acme-user",
			UserGroups: "x-acme-groups",
			Bypass:     "x acme bypass",
		},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Bypass")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsBypassEqualsUserID(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{
			UserID:     "x-acme-identity",
			UserGroups: "x-acme-groups",
			Bypass:     "x-acme-identity",
		},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Bypass")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsBypassEqualsUserGroupsCaseInsensitive(
	t *testing.T,
) {
	c := AuthzConfig{
		Enabled: true,
		Headers: AuthzHeaders{
			UserID:     "x-acme-user",
			UserGroups: "X-Acme-Groups",
			Bypass:     "x-acme-groups",
		},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Bypass")
}

// --- AuthzConfig.ValidateWithContext: enabled=true, Input ---

func TestAuthzConfig_ValidateWithContext_Enabled_DefaultInput_Valid(t *testing.T) {
	c := AuthzConfig{Enabled: true}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_AcceptsExplicitDistinctInputKeys(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "acme_user",
			Groups:   "acme_groups",
			Server:   "acme_server",
			Tool:     "acme_tool",
			Tools:    "acme_tools",
			ToolName: "acme_tool_name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

// input.* は headers.* / decisionPath.* / opaURL / timeout と同様、ゼロ値は
// 「未設定」を意味し WithDefaults が既定値で補完する。空文字を明示しても
// validation エラーにはならない。

func TestAuthzConfig_ValidateWithContext_Enabled_EmptyInputUser_FallsBackToDefault(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "",
			Groups:   "acme_groups",
			Server:   "acme_server",
			Tool:     "acme_tool",
			Tools:    "acme_tools",
			ToolName: "acme_tool_name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_EmptyInputGroups_FallsBackToDefault(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "acme_user",
			Groups:   "",
			Server:   "acme_server",
			Tool:     "acme_tool",
			Tools:    "acme_tools",
			ToolName: "acme_tool_name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_EmptyInputServer_FallsBackToDefault(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "acme_user",
			Groups:   "acme_groups",
			Server:   "",
			Tool:     "acme_tool",
			Tools:    "acme_tools",
			ToolName: "acme_tool_name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_EmptyInputTool_FallsBackToDefault(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "acme_user",
			Groups:   "acme_groups",
			Server:   "acme_server",
			Tool:     "",
			Tools:    "acme_tools",
			ToolName: "acme_tool_name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_EmptyInputTools_FallsBackToDefault(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "acme_user",
			Groups:   "acme_groups",
			Server:   "acme_server",
			Tool:     "acme_tool",
			Tools:    "",
			ToolName: "acme_tool_name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_EmptyInputToolName_FallsBackToDefault(
	t *testing.T,
) {
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "acme_user",
			Groups:   "acme_groups",
			Server:   "acme_server",
			Tool:     "acme_tool",
			Tools:    "acme_tools",
			ToolName: "",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputUserEqualsGroups(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{User: "identity", Groups: "identity"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Groups")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputUserEqualsServer(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{User: "identity", Server: "identity"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Server")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputGroupsEqualsServer(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{Groups: "same", Server: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Server")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputUserEqualsTool(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{User: "same", Tool: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Tool")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputGroupsEqualsTool(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{Groups: "same", Tool: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Tool")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputServerEqualsTool(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{Server: "same", Tool: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Tool")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputUserEqualsTools(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{User: "same", Tools: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Tools")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputGroupsEqualsTools(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{Groups: "same", Tools: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Tools")
}

func TestAuthzConfig_ValidateWithContext_Enabled_RejectsInputServerEqualsToolName(t *testing.T) {
	c := AuthzConfig{
		Enabled: true,
		Input:   AuthzInput{Server: "same", ToolName: "same"},
	}
	err := c.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "ToolName")
}

func TestAuthzConfig_ValidateWithContext_Enabled_AllowsToolAndToolsSharingName(t *testing.T) {
	// tool（call の単一ツール名キー）と tools（list のツール配列キー）は同じ
	// JSON オブジェクトに同居しないため、衝突チェックの対象外。
	c := AuthzConfig{
		Enabled: true,
		Input: AuthzInput{
			User:     "user",
			Groups:   "groups",
			Server:   "server",
			Tool:     "same",
			Tools:    "same",
			ToolName: "name",
		},
	}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}
