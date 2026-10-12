package config

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	// Guardrail を設定すると、記録する引数を外部のガードレールに通し、
	// マスキングした結果だけを書く。
	Guardrail *AuditGuardrailConfig `mapstructure:"guardrail"`
}

// 監査ログのガードレールの既定値。
const (
	DefaultAuditGuardrailTimeout     = 5 * time.Second
	DefaultAuditGuardrailQueueSize   = 1024
	DefaultAuditGuardrailConcurrency = 8
)

// AuditGuardrailConfig は監査ログに書く引数をマスキングするガードレール
// （audit.guardrail）。インターフェースは LiteLLM の Generic Guardrail API
// （https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api）に固定し、
// URL へ POST する。ガードレールの API には標準化された仕様が無いため、
// LiteLLM 向けに作られたガードレールのサービスをそのまま使えるこの形を採る。
//
// ガードレールの呼び出しと書き込みはツール呼び出しとは非同期に行うので、
// ツール呼び出しの遅延は増えない。
type AuditGuardrailConfig struct {
	// URL はガードレールのエンドポイント（LiteLLM 互換のサービスなら
	// <base>/beta/litellm_basic_guardrail_api）。
	URL string `mapstructure:"url"`
	// Headers はリクエストに付けるヘッダー（認証用のトークン等）。
	Headers map[string]string `mapstructure:"headers"`
	// Timeout は1回の呼び出しのタイムアウト（既定 5s）。
	Timeout time.Duration `mapstructure:"timeout"`
	// QueueSize は書き込み待ちの監査記録の上限（既定 1024）。溢れた記録は
	// 引数を書かずに記録する。
	QueueSize int `mapstructure:"queueSize"`
	// Concurrency はガードレールを同時に呼び出す数（既定 8）。
	Concurrency int `mapstructure:"concurrency"`
}

// TimeoutOrDefault returns Timeout, falling back to DefaultAuditGuardrailTimeout.
func (c AuditGuardrailConfig) TimeoutOrDefault() time.Duration {
	if c.Timeout <= 0 {
		return DefaultAuditGuardrailTimeout
	}
	return c.Timeout
}

// QueueSizeOrDefault returns QueueSize, falling back to DefaultAuditGuardrailQueueSize.
func (c AuditGuardrailConfig) QueueSizeOrDefault() int {
	if c.QueueSize <= 0 {
		return DefaultAuditGuardrailQueueSize
	}
	return c.QueueSize
}

// ConcurrencyOrDefault returns Concurrency, falling back to
// DefaultAuditGuardrailConcurrency.
func (c AuditGuardrailConfig) ConcurrencyOrDefault() int {
	if c.Concurrency <= 0 {
		return DefaultAuditGuardrailConcurrency
	}
	return c.Concurrency
}

func (c AuditGuardrailConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.URL, validation.Required, validation.By(validateHTTPURL)),
		validation.Field(&c.Headers, validation.By(func(value any) error {
			m, _ := value.(map[string]string)
			for name := range m {
				if err := validateHTTPHeaderName(name); err != nil {
					return fmt.Errorf("%q: %w", name, err)
				}
			}
			return nil
		})),
		validation.Field(&c.Timeout, validation.Min(time.Duration(0))),
		validation.Field(&c.QueueSize, validation.Min(0)),
		validation.Field(&c.Concurrency, validation.Min(0)),
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
		// ガードレールが通すのは引数なので、引数を記録しない設定では意味が無い。
		validation.Field(&c.Guardrail, validation.When(c.Guardrail != nil && !c.IncludeArguments,
			validation.Nil.Error("requires includeArguments: true"),
		)),
	)
}
