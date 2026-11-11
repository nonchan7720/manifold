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
	// GuardrailModeResourceRead は resources/read の結果を呼び出し元へ返す前に検査する。
	// mode に明示したときだけ有効（未設定の既定には含めない）。
	GuardrailModeResourceRead = "resource_read"
)

// DefaultGuardrailTimeout は guardrail.timeout の既定値。
const DefaultGuardrailTimeout = 5 * time.Second

// DefaultGuardrailFilesMaxBytes は guardrail.files.maxBytes の既定値。ガードレールへの
// リクエストは base64 で 4/3 倍になるので、同梱のサイドカーが受け付ける本文（16MiB）に
// 収まる大きさにしている。
const DefaultGuardrailFilesMaxBytes = 8 << 20

// GuardrailFilesConfig は結果に含まれるファイル（画像・音声・リソースの blob）の
// 検査（guardrail.files）。
type GuardrailFilesConfig struct {
	// Enabled が true なら、検査する結果のファイルをガードレールへ送る。
	// false（既定）ならファイルは検査せずに通す。
	Enabled bool `mapstructure:"enabled"`
	// MaxBytes は1回の呼び出しで送るファイルの合計バイト数の上限
	// （既定 8MiB）。超えた呼び出しは検査できないので拒否する。
	MaxBytes int64 `mapstructure:"maxBytes"`
}

// MaxBytesOrDefault returns MaxBytes, falling back to
// DefaultGuardrailFilesMaxBytes.
func (c GuardrailFilesConfig) MaxBytesOrDefault() int64 {
	if c.MaxBytes <= 0 {
		return DefaultGuardrailFilesMaxBytes
	}
	return c.MaxBytes
}

// GuardrailConfig は tools/call（mode に resource_read があれば resources/read にも）に
// 掛けるガードレール（guardrail）。
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
	// Mode はガードレールを掛けるタイミング（pre_call / post_call /
	// resource_read）。未設定なら pre_call と post_call。
	Mode []string `mapstructure:"mode"`
	// Files は結果に含まれるファイルの検査。
	Files GuardrailFilesConfig `mapstructure:"files"`
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

// ResourceRead reports whether the guardrail checks resources/read results.
// Unlike pre_call / post_call it is off unless listed in Mode.
func (c GuardrailConfig) ResourceRead() bool {
	return slices.Contains(c.Mode, GuardrailModeResourceRead)
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
			validation.Each(validation.In(
				GuardrailModePreCall, GuardrailModePostCall, GuardrailModeResourceRead,
			)),
		),
		validation.Field(&c.Files, validation.By(func(value any) error {
			if f, _ := value.(GuardrailFilesConfig); f.MaxBytes < 0 {
				return fmt.Errorf("maxBytes: must be no less than 0")
			}
			return nil
		})),
	)
}
