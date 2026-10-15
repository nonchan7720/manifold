package config

import (
	"context"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	// DefaultReadHeaderTimeout はリクエストヘッダー読み取りの上限（Slowloris 対策）。
	DefaultReadHeaderTimeout = 10 * time.Second
	// DefaultIdleTimeout は keep-alive 接続のアイドル上限。
	DefaultIdleTimeout = 120 * time.Second
)

// TimeoutsConfig は gateway の http.Server に設定するタイムアウト。
//
// MCP の Streamable HTTP / SSE や edge の WebSocket は長時間接続のため、
// Read / Write はデフォルトで無制限（0）にしている。ReadHeader と Idle のみ
// デフォルトを持つ。0（未設定）はデフォルト値（Read / Write は無制限）、
// 負の値は無制限を意味する。
type TimeoutsConfig struct {
	ReadHeader time.Duration `mapstructure:"readHeader"`
	Read       time.Duration `mapstructure:"read"`
	Write      time.Duration `mapstructure:"write"`
	Idle       time.Duration `mapstructure:"idle"`
}

// WithDefaults は未設定（0）の項目をデフォルト値で埋め、負の値を 0（無制限）に正規化したコピーを返す。
func (c TimeoutsConfig) WithDefaults() TimeoutsConfig {
	if c.ReadHeader == 0 {
		c.ReadHeader = DefaultReadHeaderTimeout
	}
	if c.Idle == 0 {
		c.Idle = DefaultIdleTimeout
	}
	c.ReadHeader = max(c.ReadHeader, 0)
	c.Read = max(c.Read, 0)
	c.Write = max(c.Write, 0)
	c.Idle = max(c.Idle, 0)
	return c
}

func (c TimeoutsConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c)
}
