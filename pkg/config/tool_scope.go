package config

import (
	"context"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// 既定値（toolScope 設定省略時）。
const (
	DefaultToolScopeHeaderServices = "x-tool-scope-services"
	DefaultToolScopeHeaderServers  = "x-tool-scope-servers"
)

// ToolScopeHeaders は、リクエスト（= LLM の 1 ターン）ごとに有効にするサービス /
// サーバーをクライアントが指定する受信ヘッダー名。値はカンマ区切りの一覧で、
// ヘッダーを複数回付けた場合はすべての値を合わせた一覧になる。
type ToolScopeHeaders struct {
	// Services はサービスコード（mcpServers.<name>.service.code。未設定なら
	// サーバー名）の一覧を受け取るヘッダー。
	Services string `mapstructure:"services"`
	// Servers はサーバー名（mcpServers / agents のキー）の一覧を受け取るヘッダー。
	Servers string `mapstructure:"servers"`
}

// ToolScopeConfig は、authz（OPA）とは独立に、クライアントがリクエストごとに
// 使うツールをサービス / サーバー単位でさらに絞り込む機能の設定。
//
// 絞り込みは許可を広げることはなく、authz が許可したツールとの積になる。
// ヘッダーが付いていないリクエストは絞り込まれない（既存動作のまま）。
// 既定は無効（Enabled: false）。
type ToolScopeConfig struct {
	Enabled bool             `mapstructure:"enabled"`
	Headers ToolScopeHeaders `mapstructure:"headers"`
}

// WithDefaults は空のフィールドを既定値で埋めたコピーを返す。
func (c ToolScopeConfig) WithDefaults() ToolScopeConfig {
	if c.Headers.Services == "" {
		c.Headers.Services = DefaultToolScopeHeaderServices
	}
	if c.Headers.Servers == "" {
		c.Headers.Servers = DefaultToolScopeHeaderServers
	}
	return c
}

func (c ToolScopeConfig) ValidateWithContext(ctx context.Context) error {
	if !c.Enabled {
		return nil
	}
	c = c.WithDefaults()
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Headers),
	)
}

func (c ToolScopeHeaders) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Services, validation.By(validateHTTPHeaderName)),
		validation.Field(&c.Servers,
			validation.By(validateHTTPHeaderName),
			validation.By(validateDiffersFrom("headers.services", c.Services)),
		),
	)
}
