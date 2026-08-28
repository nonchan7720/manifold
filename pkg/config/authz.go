package config

import (
	"context"
	"fmt"
	"net/url"
	"slices"
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

// Value types accepted by AuthzInputHeaderField.Type, naming the JSON type
// the header's raw value becomes in the decision input. An empty Type is a
// synonym for AuthzInputFieldTypeString.
const (
	AuthzInputFieldTypeString = "string"
	AuthzInputFieldTypeList   = "list"
	AuthzInputFieldTypeNumber = "number"
)

// AuthzInputHeaderField describes one entry of AuthzInput.FromHeaders: the
// inbound header to read, whether the request is denied when it is absent,
// and how the raw value is turned into a JSON value.
type AuthzInputHeaderField struct {
	Header   string `mapstructure:"header"`
	Required *bool  `mapstructure:"required"`
	Type     string `mapstructure:"type"`
}

// IsRequired reports whether a missing or empty header denies the request.
// Unset (nil) means required, so omitting the key stays fail-closed while
// still letting an explicit false through.
func (f AuthzInputHeaderField) IsRequired() bool {
	return f.Required == nil || *f.Required
}

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

	// FromHeaders maps a decision-input field name to the inbound HTTP
	// header it is read from. Resolved values are added as top-level
	// fields to every decision input, typed per AuthzInputHeaderField.Type.
	// Empty (the default) adds nothing; a required header missing or empty
	// on a request denies (authz.ErrMissingInputHeader), while an optional
	// one is simply left out of the input.
	FromHeaders map[string]AuthzInputHeaderField `mapstructure:"fromHeaders"`
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
		validation.Field(&c.FromHeaders, validation.By(c.validateFromHeaders)),
	)
}

// validateFromHeaders rejects an empty field name, an empty or invalid
// header name, an unknown value type, and a field name equal to an
// already-resolved top-level input key (c.User/Groups/Server/Tool/Tools —
// the renamed value, not the original mapstructure key). c.ToolName is not
// reserved: it only names a key inside the tools array elements, never a
// top-level one, so it cannot collide. The comparison is case-sensitive
// because OPA input keys are. The same header may be assigned to more than
// one field.
func (c AuthzInput) validateFromHeaders(value any) error {
	m, _ := value.(map[string]AuthzInputHeaderField)
	reserved := []string{c.User, c.Groups, c.Server, c.Tool, c.Tools}
	for field, spec := range m {
		if field == "" {
			return fmt.Errorf("field name must not be empty")
		}
		if !httpguts.ValidHeaderFieldName(spec.Header) {
			return fmt.Errorf(
				"header name for field %q must be a valid, non-empty HTTP header field name", field,
			)
		}
		switch spec.Type {
		case "", AuthzInputFieldTypeString, AuthzInputFieldTypeList, AuthzInputFieldTypeNumber:
		default:
			return fmt.Errorf(
				"type for field %q must be one of %q, %q, %q (or empty)",
				field,
				AuthzInputFieldTypeString, AuthzInputFieldTypeList, AuthzInputFieldTypeNumber,
			)
		}
		if slices.Contains(reserved, field) {
			return fmt.Errorf("field name %q collides with an existing input key", field)
		}
	}
	return nil
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
