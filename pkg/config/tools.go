package config

import (
	"context"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
)

// toolNameRegex は MCP のツール名として受け付ける文字集合と長さ
// （MCP 仕様の推奨: 1〜128 文字の英数字・'_'・'-'・'.'）。
var toolNameRegex = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

// ToolsConfig groups the per-server tool settings under mcpServers.<name>.tools:
// the static tool catalog (File) and which tools are exposed and under what
// name (Include / Exclude / Overrides).
//
// Include / Exclude are path.Match glob patterns matched against the tool's
// original name (the name the backend or the OpenAPI spec gives it, before
// any override). A tool is exposed when it matches at least one Include
// pattern (or Include is empty) and no Exclude pattern.
//
// On an OpenAPI / Swagger server, Exclude is additionally applied while the
// catalog is built, matched against the operation's operationId as well as
// the tool name derived from it: a matching operation is never turned into
// a tool, so it is absent from the generated catalog ("manifold openapi
// tools", tools.file, "openapi diff"), not merely hidden from tools/list.
//
// Overrides is keyed by the original tool name and renames the tool and/or
// replaces its description. A renamed tool is only reachable under its new
// name.
//
// Tools the gateway registers itself (a reverse server's create_pairing_code)
// are not the backend's and are left alone by all three.
type ToolsConfig struct {
	File string `mapstructure:"file"`

	Include   []string                `mapstructure:"include"`
	Exclude   []string                `mapstructure:"exclude"`
	Overrides map[string]ToolOverride `mapstructure:"overrides"`

	// ResultFilter は、呼び出し元がツールの結果に JMESPath の式を適用して、必要な部分
	// だけを受け取れるようにする設定。
	ResultFilter *ResultFilterConfig `mapstructure:"resultFilter"`
}

// DefaultResultFilterArgument は ResultFilterConfig.Argument の既定値。
const DefaultResultFilterArgument = "_jmespath"

// argumentNameRegex は ResultFilterConfig.Argument に使える名前。MCP クライアントが
// input schema のプロパティ名として受け付ける文字集合と長さ。
var argumentNameRegex = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// ResultFilterConfig は mcpServers.<name>.tools.resultFilter の設定。
//
// 有効にすると、ゲートウェイは対象のツールの input schema に Argument の名前の引数を
// 足す。呼び出し元がそこへ JMESPath の式を渡すと、ゲートウェイはその引数を取り除いて
// バックエンドを呼び、結果の JSON に式を適用したものだけを返す。
//
// 式でできるのは、返すフィールドを選ぶこと。条件で絞る構文と、位置・件数で絞る構文は
// Features でツールごとに有効にする。集計や変換などの関数は使えない。
//
// Include / Exclude は対象のツールを絞る glob で、ToolsConfig の Include / Exclude と
// 同じく元のツール名に照合する。どちらも空なら、サーバー自身の全ツールが対象になる。
type ResultFilterConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Argument は式を渡す引数の名前。未設定なら DefaultResultFilterArgument。
	// バックエンドのツールが同じ名前の引数を持つ場合はゲートウェイの引数が優先される
	// ので、衝突するならここで別の名前にする。
	Argument string   `mapstructure:"argument"`
	Include  []string `mapstructure:"include"`
	Exclude  []string `mapstructure:"exclude"`
	// Features は、フィールドの選択に加えて使える構文を、ツールごとに有効にする。
	Features ResultFilterFeatures `mapstructure:"features"`
}

// ResultFilterFeatures は、式で使える追加の構文ごとに、それを使えるツールを並べる。
// 値は元のツール名に照合する glob で、空ならどのツールでも使えない（["*"] で全ツール）。
type ResultFilterFeatures struct {
	// Conditions は条件で絞る構文（items[?state=='open']。比較と論理の演算子を含む）を
	// 使えるツール。
	Conditions []string `mapstructure:"conditions"`
	// Indexing は位置と件数で絞る構文（items[0]、items[:5]）を使えるツール。
	Indexing []string `mapstructure:"indexing"`
}

// ArgumentName は式を渡す引数の名前を返す。
func (c *ResultFilterConfig) ArgumentName() string {
	if c == nil || c.Argument == "" {
		return DefaultResultFilterArgument
	}
	return c.Argument
}

// Applies は元の名前が name のツールで結果の絞り込みを受け付けるかを返す。
func (c *ResultFilterConfig) Applies(name string) bool {
	return c != nil && c.Enabled && matchesPatterns(c.Include, c.Exclude, name)
}

// ConditionsEnabled は元の名前が name のツールで、条件で絞る構文を使えるかを返す。
func (c *ResultFilterConfig) ConditionsEnabled(name string) bool {
	return c.Applies(name) && matchAny(c.Features.Conditions, name)
}

// IndexingEnabled は元の名前が name のツールで、位置と件数で絞る構文を使えるかを返す。
func (c *ResultFilterConfig) IndexingEnabled(name string) bool {
	return c.Applies(name) && matchAny(c.Features.Indexing, name)
}

func (c ResultFilterConfig) ValidateWithContext(context.Context) error {
	if c.Argument != "" && !argumentNameRegex.MatchString(c.Argument) {
		return fmt.Errorf(
			"argument %q must be 1-64 characters of letters, digits, '_', '-' or '.'", c.Argument,
		)
	}
	if err := validatePatterns(c.Include, c.Exclude); err != nil {
		return err
	}
	if err := validatePatterns(c.Features.Conditions, c.Features.Indexing); err != nil {
		return fmt.Errorf("features: %w", err)
	}
	return nil
}

// ToolOverride replaces the name and/or description a tool is exposed with.
// Empty fields keep the original value.
type ToolOverride struct {
	// Tool は上書き対象の元のツール名。未設定ならキーを元のツール名として使う。
	// 設定ローダーは viper が小文字化したキーを設定ファイルの表記へ戻すので、
	// 大文字を含むツール名もキーのまま書ける。
	Tool        string `mapstructure:"tool"`
	Name        string `mapstructure:"name"`
	Description string `mapstructure:"description"`
}

// HasFilter reports whether any of Include / Exclude / Overrides is set, i.e.
// whether the exposed tool list can differ from the backend's.
func (c *ToolsConfig) HasFilter() bool {
	if c == nil {
		return false
	}
	return len(c.Include) > 0 || len(c.Exclude) > 0 || len(c.Overrides) > 0
}

// Allowed reports whether a tool with the original name is exposed by the
// Include / Exclude patterns. Patterns are validated at load time, so a
// malformed pattern (never reached in practice) simply doesn't match.
func (c *ToolsConfig) Allowed(name string) bool {
	if c == nil {
		return true
	}
	return matchesPatterns(c.Include, c.Exclude, name)
}

// ResolvedOverrides returns Overrides keyed by the original tool name (the
// override's Tool field when set, else its map key).
func (c *ToolsConfig) ResolvedOverrides() map[string]ToolOverride {
	if c == nil || len(c.Overrides) == 0 {
		return nil
	}
	out := make(map[string]ToolOverride, len(c.Overrides))
	for key, override := range c.Overrides {
		original := key
		if override.Tool != "" {
			original = override.Tool
		}
		out[original] = override
	}
	return out
}

// Excluded reports whether name matches an Exclude pattern. Unlike Allowed
// it ignores Include, so an OpenAPI catalog builder can drop excluded
// operations while leaving Include to the exposure filter. Matching is
// case-sensitive.
func (c *ToolsConfig) Excluded(name string) bool {
	if c == nil {
		return false
	}
	return matchAny(c.Exclude, name)
}

func (c ToolsConfig) ValidateWithContext(ctx context.Context) error {
	if err := validatePatterns(c.Include, c.Exclude); err != nil {
		return err
	}
	if c.ResultFilter != nil {
		if err := c.ResultFilter.ValidateWithContext(ctx); err != nil {
			return fmt.Errorf("resultFilter: %w", err)
		}
	}
	renamed := map[string]string{}
	originals := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(c.Overrides)) {
		override := c.Overrides[key]
		original := key
		if override.Tool != "" {
			original = override.Tool
		}
		if prev, dup := originals[original]; dup {
			return fmt.Errorf("overrides %q and %q both target tool %q", prev, key, original)
		}
		originals[original] = key
		if override.Name == "" {
			continue
		}
		if !toolNameRegex.MatchString(override.Name) {
			return fmt.Errorf(
				"overrides %q: name %q must be 1-128 characters of letters, digits, '_', '-' or '.'",
				key,
				override.Name,
			)
		}
		if prev, dup := renamed[override.Name]; dup {
			return fmt.Errorf(
				"overrides %q and %q both rename to %q", prev, key, override.Name,
			)
		}
		renamed[override.Name] = key
	}
	return nil
}

// validatePatterns は include / exclude の glob が path.Match の構文に合うかを検査する。
func validatePatterns(include, exclude []string) error {
	for _, pattern := range slices.Concat(include, exclude) {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid tool pattern %q: %w", pattern, err)
		}
	}
	return nil
}

// matchesPatterns は name が include のいずれかに一致し（include が空なら常に一致）、
// exclude のどれにも一致しないかを返す。
func matchesPatterns(include, exclude []string, name string) bool {
	if len(include) > 0 && !matchAny(include, name) {
		return false
	}
	return !matchAny(exclude, name)
}

func matchAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}
