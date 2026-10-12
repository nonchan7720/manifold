package config

import (
	"context"
	"fmt"
	"slices"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// ガードレールを掛けるタイミング（guardrail.mode）。LiteLLM の mode と同じ名前。
const (
	// GuardrailModePreCall は tools/call の引数をバックエンドへ送る前に検査する。
	GuardrailModePreCall = "pre_call"
	// GuardrailModePostCall は tools/call の結果を呼び出し元へ返す前に検査する。
	GuardrailModePostCall = "post_call"
)

// DefaultGuardrailTimeout は guardrail.timeout の既定値。
const DefaultGuardrailTimeout = 5 * time.Second

// GuardrailConfig は tools/call に掛けるガードレール（guardrail）。
// インターフェースは LiteLLM の Generic Guardrail API
// （https://docs.litellm.ai/docs/adding_provider/generic_guardrail_api）に固定し、
// authz の OPA と同じく呼び出しのたびに URL へ POST する。ガードレールの API には
// 標準化された仕様が無いため、LiteLLM 向けに作られたガードレールのサービスを
// そのまま使えるこの形を採る。AWS Bedrock Guardrails は同梱のサイドカー
// （manifold guardrail bedrock）を挟んで使う。
type GuardrailConfig struct {
	Enabled bool `mapstructure:"enabled"`
	// URL はガードレールのエンドポイント（LiteLLM 互換のサービスなら
	// <base>/beta/litellm_basic_guardrail_api）。
	URL string `mapstructure:"url"`
	// Headers はリクエストに付けるヘッダー（認証用のトークン等）。
	Headers map[string]string `mapstructure:"headers"`
	// Timeout は1回の呼び出しのタイムアウト（既定 5s）。
	Timeout time.Duration `mapstructure:"timeout"`
	// Mode はガードレールを掛けるタイミング（pre_call / post_call）。
	// 未設定なら両方。
	Mode []string `mapstructure:"mode"`
}

// TimeoutOrDefault returns Timeout, falling back to DefaultGuardrailTimeout.
func (c GuardrailConfig) TimeoutOrDefault() time.Duration {
	if c.Timeout <= 0 {
		return DefaultGuardrailTimeout
	}
	return c.Timeout
}

// PreCall reports whether the guardrail checks tools/call arguments.
func (c GuardrailConfig) PreCall() bool {
	return len(c.Mode) == 0 || slices.Contains(c.Mode, GuardrailModePreCall)
}

// PostCall reports whether the guardrail checks tools/call results.
func (c GuardrailConfig) PostCall() bool {
	return len(c.Mode) == 0 || slices.Contains(c.Mode, GuardrailModePostCall)
}

func (c GuardrailConfig) ValidateWithContext(ctx context.Context) error {
	if !c.Enabled {
		return nil
	}
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
		validation.Field(&c.Mode,
			validation.Each(validation.In(GuardrailModePreCall, GuardrailModePostCall)),
		),
	)
}
