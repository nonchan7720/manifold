package config

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// audit.output が取る特別な値。それ以外はファイルパスとして扱う。
const (
	AuditOutputStdout = "stdout"
	AuditOutputStderr = "stderr"
)

// AuditConfig writes one JSON line per tools/call (who called which tool on
// which server, how long it took and how it ended) to Output.
type AuditConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// Output は stdout / stderr / ファイルパス。未設定は stderr。
	Output string `mapstructure:"output"`
	// IncludeArguments が true なら tools/call の引数も記録する。引数には
	// 個人情報や秘密情報が含まれうるため既定では記録しない。
	IncludeArguments bool `mapstructure:"includeArguments"`
	// Redact は記録する引数とエラーメッセージから秘密情報を伏せる設定。
	Redact AuditRedactConfig `mapstructure:"redact"`
}

// AuditRedactReplacement は伏せた値の置き換え先。
const AuditRedactReplacement = "[REDACTED]"

// DefaultAuditRedactKeys は既定で値を伏せる引数のキー名。大文字小文字と
// "-"・"_"・"." を無視し、キー名がこれらで終わる値を伏せる（githubToken・
// client_secret・X-Api-Key 等）。
var DefaultAuditRedactKeys = []string{
	"password", "passwd", "secret", "token", "apikey", "authorization",
	"cookie", "privatekey", "credential", "credentials",
}

// AuditRedactConfig は監査ログに書く前に秘密情報を伏せる設定（audit.redact）。
// 外部を呼ばずプロセス内で処理する。
type AuditRedactConfig struct {
	// Keys は DefaultAuditRedactKeys に加えて値を伏せる引数のキー名。
	Keys []string `mapstructure:"keys"`
	// Patterns は引数の文字列値とエラーメッセージから伏せる正規表現（Go の RE2）。
	Patterns []string `mapstructure:"patterns"`
}

func (c AuditRedactConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Keys, validation.Each(validation.Required)),
		validation.Field(&c.Patterns, validation.Each(validation.Required, validation.By(
			func(value any) error {
				v, _ := value.(string)
				if _, err := regexp.Compile(v); err != nil {
					return fmt.Errorf("invalid regular expression: %w", err)
				}
				return nil
			},
		))),
	)
}

// OutputOrDefault returns Output, falling back to stderr when unset.
func (c AuditConfig) OutputOrDefault() string {
	if strings.TrimSpace(c.Output) == "" {
		return AuditOutputStderr
	}
	return c.Output
}

func (c AuditConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Output, validation.By(func(value any) error {
			v, _ := value.(string)
			if v != "" && strings.TrimSpace(v) == "" {
				return fmt.Errorf("must not be blank")
			}
			return nil
		})),
		validation.Field(&c.Redact),
	)
}
