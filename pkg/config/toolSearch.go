package config

import (
	"context"
	"fmt"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// DefaultToolSearchThreshold is used when ToolSearchConfig.Threshold is unset (<= 0).
// 呼び出し元に見えるツール数がエンドポイント単位でこの値を超えると、そのエンドポイントの
// tools/list は合成ツール tool_search のみを返すようになる。
const DefaultToolSearchThreshold = 100

// DefaultToolSearchLimit is used when ToolSearchConfig.DefaultLimit is unset (<= 0).
// tool_search 呼び出し時に limit が指定されなかった場合の検索結果件数上限。
const DefaultToolSearchLimit = 10

// ToolSearchResultFormatDefault は tool_search の検索結果を []ToolDef
// （name/description/inputSchema）で返すフォーマット。ResultFormat の既定値。
const ToolSearchResultFormatDefault = "default"

// ToolSearchResultFormatClaude は Claude API の Tool Search Tool のカスタム検索実装規約
// (https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool#custom-tool-search-implementation)
// に準拠した tool_reference ブロック（{"type":"tool_reference","tool_name":"..."}）で返すフォーマット。
const ToolSearchResultFormatClaude = "claude"

// DefaultToolSearchDigestMaxTools is used when ToolSearchConfig.DigestMaxTools is
// unset (0). -1 means "list every visible tool in the tool_search description
// digest" (no truncation).
const DefaultToolSearchDigestMaxTools = -1

// ToolSearchConfig controls the tool_search fallback: once the caller can see
// more than Threshold tools on an endpoint, that endpoint's tools/list response
// is replaced by a single synthetic tool_search tool that the client queries
// for the full tool definitions (name / description / inputSchema) and then
// calls directly. The decision and the search both work on the tools the
// caller can see after tools.include / exclude / overrides and authz.
type ToolSearchConfig struct {
	// Threshold is the number of visible tools on an endpoint above which
	// tool_search replaces the real tool list. 0 (or unset) falls back to
	// DefaultToolSearchThreshold.
	Threshold int `mapstructure:"threshold"`

	// DefaultLimit is the default number of results returned by tool_search when the
	// caller does not specify a limit. 0 (or unset) falls back to DefaultToolSearchLimit.
	DefaultLimit int `mapstructure:"defaultLimit"`

	// ResultFormat selects the shape of tool_search's results: ToolSearchResultFormatDefault
	// ([]ToolDef, the default) or ToolSearchResultFormatClaude ([]ToolReference, compatible
	// with the Claude API's Tool Search Tool custom implementation contract). Empty (unset)
	// falls back to ToolSearchResultFormatDefault.
	ResultFormat string `mapstructure:"resultFormat"`

	// DigestMaxTools caps how many of the visible tools (sorted by name) are
	// listed in tool_search's description digest. -1 or 0 (unset) lists every
	// tool; a positive N lists the first N by name and notes the omission.
	DigestMaxTools int `mapstructure:"digestMaxTools"`
}

// WithDefaults returns a copy of c with zero-value (or negative) fields replaced by defaults.
func (c ToolSearchConfig) WithDefaults() ToolSearchConfig {
	if c.Threshold <= 0 {
		c.Threshold = DefaultToolSearchThreshold
	}
	if c.DefaultLimit <= 0 {
		c.DefaultLimit = DefaultToolSearchLimit
	}
	if c.ResultFormat == "" {
		c.ResultFormat = ToolSearchResultFormatDefault
	}
	if c.DigestMaxTools == 0 {
		c.DigestMaxTools = DefaultToolSearchDigestMaxTools
	}
	return c
}

// ValidateWithContext validates ToolSearchConfig. Negative Threshold / DefaultLimit
// values are rejected (0 means "use the default"). ResultFormat, if non-empty, must
// be one of the known values. DigestMaxTools must be -1, 0 (both "all tools") or
// positive.
func (c ToolSearchConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&c,
		validation.Field(&c.Threshold, validation.Min(0)),
		validation.Field(&c.DefaultLimit, validation.Min(0)),
		validation.Field(&c.ResultFormat,
			validation.When(c.ResultFormat != "",
				validation.In(ToolSearchResultFormatDefault, ToolSearchResultFormatClaude),
			),
		),
		validation.Field(&c.DigestMaxTools, validation.By(validateDigestMaxTools)),
	)
}

// validateDigestMaxTools rejects everything below -1.
func validateDigestMaxTools(value any) error {
	v, ok := value.(int)
	if !ok {
		return fmt.Errorf("must be an int")
	}
	if v < -1 {
		return fmt.Errorf("must be -1 (all), 0 (all) or a positive number")
	}
	return nil
}
