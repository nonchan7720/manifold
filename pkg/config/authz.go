package config

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"golang.org/x/net/http/httpguts"
)

// 既定値（authz 設定省略時）。
const (
	DefaultAuthzOPAURL = "http://localhost:8181"

	DefaultAuthzTimeout = 3 * time.Second

	DefaultAuthzDecisionPathList    = "/v1/data/mcp/authz/allowed_tools"
	DefaultAuthzDecisionPathCall    = "/v1/data/mcp/authz/allow"
	DefaultAuthzDecisionPathCatalog = "/v1/data/mcp/authz/allow_catalog"

	DefaultAuthzHeaderUserID     = "x-user-id"
	DefaultAuthzHeaderUserGroups = "x-user-groups"
	DefaultAuthzHeaderBypass     = "x-authz-bypass" //nolint: gosec // header name, not a credential

	DefaultAuthzInputUser     = "user"
	DefaultAuthzInputGroups   = "groups"
	DefaultAuthzInputServer   = "server"
	DefaultAuthzInputTool     = "tool"
	DefaultAuthzInputTools    = "tools"
	DefaultAuthzInputToolName = "name"
)

// AuthzDecisionPath is the OPA data path queried for each decision kind (see
// docs/design/opa-tool-authorization-plan.ja.md「動作」).
type AuthzDecisionPath struct {
	List    string `mapstructure:"list"`
	Call    string `mapstructure:"call"`
	Catalog string `mapstructure:"catalog"`
}

// AuthzInput names the JSON keys Manifold uses when building the OPA
// decision input (see OPADecider.Allow / AllowedTools / AllowCatalog).
// Configurable so a policy author can match an existing input contract
// instead of Manifold's defaults.
type AuthzInput struct {
	User     string `mapstructure:"user"`
	Groups   string `mapstructure:"groups"`
	Server   string `mapstructure:"server"`
	Tool     string `mapstructure:"tool"`
	Tools    string `mapstructure:"tools"`
	ToolName string `mapstructure:"toolName"`
}

// AuthzHeaders names the inbound HTTP headers an upstream identity/authn
// layer is expected to inject before Manifold sees the request.
// AuthzHeaders.Bypass is checked against the literal string "true" (see
// authz.BypassRequested); it exists so a fronting proxy can disable authz
// per-request for tenants that opt out, without flipping Enabled globally.
type AuthzHeaders struct {
	UserID     string `mapstructure:"userID"`
	UserGroups string `mapstructure:"userGroups"`
	Bypass     string `mapstructure:"bypass"`
}

// AuthzConfig configures the OPA sidecar used as the tool-call PDP.
// Disabled (Enabled: false) by default, preserving prior behavior.
type AuthzConfig struct {
	Enabled      bool              `mapstructure:"enabled"`
	OPAURL       string            `mapstructure:"opaURL"`
	Timeout      time.Duration     `mapstructure:"timeout"`
	DecisionPath AuthzDecisionPath `mapstructure:"decisionPath"`
	Headers      AuthzHeaders      `mapstructure:"headers"`
	Input        AuthzInput        `mapstructure:"input"`
}

// WithDefaults returns a copy of c with zero-value fields replaced by the
// documented defaults.
func (c AuthzConfig) WithDefaults() AuthzConfig {
	if c.OPAURL == "" {
		c.OPAURL = DefaultAuthzOPAURL
	}
	if c.Timeout == 0 {
		c.Timeout = DefaultAuthzTimeout
	}
	if c.DecisionPath.List == "" {
		c.DecisionPath.List = DefaultAuthzDecisionPathList
	}
	if c.DecisionPath.Call == "" {
		c.DecisionPath.Call = DefaultAuthzDecisionPathCall
	}
	if c.DecisionPath.Catalog == "" {
		c.DecisionPath.Catalog = DefaultAuthzDecisionPathCatalog
	}
	if c.Headers.UserID == "" {
		c.Headers.UserID = DefaultAuthzHeaderUserID
	}
	if c.Headers.UserGroups == "" {
		c.Headers.UserGroups = DefaultAuthzHeaderUserGroups
	}
	if c.Headers.Bypass == "" {
		c.Headers.Bypass = DefaultAuthzHeaderBypass
	}
	if c.Input.User == "" {
		c.Input.User = DefaultAuthzInputUser
	}
	if c.Input.Groups == "" {
		c.Input.Groups = DefaultAuthzInputGroups
	}
	if c.Input.Server == "" {
		c.Input.Server = DefaultAuthzInputServer
	}
	if c.Input.Tool == "" {
		c.Input.Tool = DefaultAuthzInputTool
	}
	if c.Input.Tools == "" {
		c.Input.Tools = DefaultAuthzInputTools
	}
	if c.Input.ToolName == "" {
		c.Input.ToolName = DefaultAuthzInputToolName
	}
	return c
}

func (c AuthzConfig) ValidateWithContext(ctx context.Context) error {
	if !c.Enabled {
		return nil
	}
	c = c.WithDefaults()
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.OPAURL, validation.By(validateHTTPURL)),
		validation.Field(&c.Timeout, validation.By(validatePositiveDuration)),
		validation.Field(&c.DecisionPath),
		validation.Field(&c.Headers),
		validation.Field(&c.Input),
	)
}

func (c AuthzDecisionPath) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.List, validation.By(validateLeadingSlash)),
		validation.Field(&c.Call, validation.By(validateLeadingSlash)),
		validation.Field(&c.Catalog, validation.By(validateLeadingSlash)),
	)
}

// ValidateWithContext rejects collisions between keys that appear together
// in the same OPA input object: user/groups/server/tool (tools/call),
// user/groups/tools (tools/list), and server/toolName (each tools/list
// array element). Empty keys are not rejected here: like headers.*,
// decisionPath.*, opaURL and timeout, a zero value means "unset" and is
// backfilled by WithDefaults before validation runs.
func (c AuthzInput) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.User),
		validation.Field(&c.Groups,
			validation.By(validateDiffersFrom("input.user", c.User)),
		),
		validation.Field(&c.Server,
			validation.By(validateDiffersFrom("input.user", c.User)),
			validation.By(validateDiffersFrom("input.groups", c.Groups)),
		),
		validation.Field(&c.Tool,
			validation.By(validateDiffersFrom("input.user", c.User)),
			validation.By(validateDiffersFrom("input.groups", c.Groups)),
			validation.By(validateDiffersFrom("input.server", c.Server)),
		),
		validation.Field(&c.Tools,
			validation.By(validateDiffersFrom("input.user", c.User)),
			validation.By(validateDiffersFrom("input.groups", c.Groups)),
		),
		validation.Field(&c.ToolName,
			validation.By(validateDiffersFrom("input.server", c.Server)),
		),
	)
}

func (c AuthzHeaders) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.UserID, validation.By(validateHTTPHeaderName)),
		validation.Field(&c.UserGroups,
			validation.By(validateHTTPHeaderName),
			validation.By(validateDiffersFrom("headers.userID", c.UserID)),
		),
		validation.Field(&c.Bypass,
			validation.By(validateHTTPHeaderName),
			validation.By(validateDiffersFrom("headers.userID", c.UserID)),
			validation.By(validateDiffersFrom("headers.userGroups", c.UserGroups)),
		),
	)
}

// validateDiffersFrom rejects a value equal (case-insensitively) to other,
// naming the field it must differ from.
func validateDiffersFrom(otherField, other string) validation.RuleFunc {
	return func(value any) error {
		s, _ := value.(string)
		if strings.EqualFold(s, other) {
			return fmt.Errorf("must differ from %s", otherField)
		}
		return nil
	}
}

func validateHTTPURL(value any) error {
	s, _ := value.(string)
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("must be a valid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if u.Host == "" {
		return fmt.Errorf("must include a host")
	}
	return nil
}

func validateHTTPHeaderName(value any) error {
	s, _ := value.(string)
	if !httpguts.ValidHeaderFieldName(s) {
		return fmt.Errorf("must be a valid HTTP header field name")
	}
	return nil
}

func validateLeadingSlash(value any) error {
	s, _ := value.(string)
	if !strings.HasPrefix(s, "/") {
		return fmt.Errorf("must start with '/'")
	}
	return nil
}

func validatePositiveDuration(value any) error {
	d, _ := value.(time.Duration)
	if d <= 0 {
		return fmt.Errorf("must be a positive duration")
	}
	return nil
}
