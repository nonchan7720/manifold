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
	if len(c.Include) > 0 && !matchAny(c.Include, name) {
		return false
	}
	return !matchAny(c.Exclude, name)
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

func (c ToolsConfig) ValidateWithContext(context.Context) error {
	for _, pattern := range slices.Concat(c.Include, c.Exclude) {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("invalid tool pattern %q: %w", pattern, err)
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

func matchAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if ok, _ := path.Match(pattern, name); ok {
			return true
		}
	}
	return false
}
