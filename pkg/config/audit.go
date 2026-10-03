package config

import (
	"context"
	"fmt"
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
	)
}
