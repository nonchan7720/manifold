package config

import (
	"context"
	"fmt"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// DefaultToolSearchThreshold is used when ToolSearchConfig.Threshold is unset (<= 0).
// 全 mcpServers 合計のツール数がこの値を超えると、各エンドポイントの tools/list は
// 合成ツール tool_search のみを返すようになる。
const DefaultToolSearchThreshold = 100

// DefaultToolSearchLimit is used when ToolSearchConfig.DefaultLimit is unset (<= 0).
// tool_search 呼び出し時に limit が指定されなかった場合の検索結果件数上限。
const DefaultToolSearchLimit = 10

// ToolSearchResultFormatDefault は tool_search の検索結果を従来どおり []ToolDef
// （name/description/inputSchema）で返すフォーマット。ResultFormat の既定値。
const ToolSearchResultFormatDefault = "default"

// ToolSearchResultFormatClaude は Claude API の Tool Search Tool のカスタム検索実装規約
// (https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool#custom-tool-search-implementation)
// に準拠した tool_reference ブロック（{"type":"tool_reference","tool_name":"..."}）で返すフォーマット。
const ToolSearchResultFormatClaude = "claude"

// DefaultToolSearchDigestMaxTools is used when ToolSearchConfig.DigestMaxTools is the Go
// zero value (0), but only via WithDefaults — see DigestMaxTools's doc comment for why an
// explicit 0 is otherwise always rejected by ValidateWithContext. -1 means "include all
// registered tools in the tool_search description digest" (no truncation).
const DefaultToolSearchDigestMaxTools = -1

// ToolSearchConfig controls the tool_search fallback feature: once the total number of
// tools across all mcpServers exceeds Threshold, each endpoint's tools/list response is
// replaced by a single synthetic tool_search tool that clients can query for the full
// tool definitions (name / description / inputSchema) and then call directly.
type ToolSearchConfig struct {
	// Threshold is the total tool count across all mcpServers above which tool_search
	// replaces the real tool list. 0 (or unset) falls back to DefaultToolSearchThreshold.
	Threshold int `mapstructure:"threshold"`

	// DefaultLimit is the default number of results returned by tool_search when the
	// caller does not specify a limit. 0 (or unset) falls back to DefaultToolSearchLimit.
	DefaultLimit int `mapstructure:"defaultLimit"`

	// ResultFormat selects the shape of tool_search's results: ToolSearchResultFormatDefault
	// ([]ToolDef, the default) or ToolSearchResultFormatClaude ([]ToolReference, compatible
	// with the Claude API's Tool Search Tool custom implementation contract). Empty (unset)
	// falls back to ToolSearchResultFormatDefault.
	ResultFormat string `mapstructure:"resultFormat"`

	// DigestMaxTools caps how many tools (out of all currently registered for an endpoint,
	// sorted by name) are listed in tool_search's description digest. -1 means "include all
	// tools" (the default, and the pre-existing behavior). A positive N caps the digest to
	// the first N tools by name; if the endpoint has fewer than N tools, all of them are
	// shown (no truncation notice).
	//
	// Unlike Threshold/DefaultLimit/ResultFormat, an explicit 0 here is NOT treated as
	// "unset" by ValidateWithContext — it is always a configuration error, since it almost
	// certainly indicates a mistake (there is no "hide the digest entirely" use case; use a
	// large negative-adjacent value is not supported either). WithDefaults still maps the Go
	// zero value 0 to -1, but only for the benefit of callers that construct ToolSearchConfig
	// directly (bypassing viper) and call WithDefaults before use; values loaded from a config
	// file or the equivalent environment variable go through ValidateWithContext first (see
	// load.go), so an explicit `digestMaxTools: 0` in config.yaml is caught as an error rather
	// than silently defaulting to -1.
	DigestMaxTools int `mapstructure:"digestMaxTools"`
}

// WithDefaults returns a copy of c with zero-value (or negative) fields replaced by defaults.
// DigestMaxTools is special-cased: only the exact Go zero value (0) is replaced by
// DefaultToolSearchDigestMaxTools (-1); other negative values are left as-is so that
// ValidateWithContext can reject them (see DigestMaxTools's doc comment).
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

// ValidateWithContext validates ToolSearchConfig. Negative Threshold/DefaultLimit values
// are rejected; 0 is accepted as a valid "not yet defaulted" sentinel (WithDefaults handles
// that separately). ResultFormat, if non-empty, must be one of the known enum values.
// DigestMaxTools must be -1 (all tools) or a positive number; unlike the other fields, 0 is
// always rejected — see DigestMaxTools's doc comment for why.
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

// validateDigestMaxTools rejects everything except -1 (all tools) and positive numbers.
func validateDigestMaxTools(value any) error {
	v, ok := value.(int)
	if !ok {
		return fmt.Errorf("must be an int")
	}
	if v == -1 || v > 0 {
		return nil
	}
	return fmt.Errorf("must be -1 (all) or a positive number")
}
