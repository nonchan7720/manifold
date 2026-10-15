package config

import (
	"context"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

const (
	// DefaultShutdownDrainDelay は ShutdownConfig.DrainDelay 未設定時の待機時間。
	DefaultShutdownDrainDelay = 5 * time.Second
	// DefaultShutdownTimeout は ShutdownConfig.Timeout 未設定時の、処理中リクエストの完了待ち上限。
	DefaultShutdownTimeout = 30 * time.Second
)

// ShutdownConfig は SIGTERM 受信後のグレースフルシャットダウンの挙動。
//
// 受信するとまず /readyz が 503 を返し始め、DrainDelay の間は通常通り
// リクエストを受け付けたまま待つ（Kubernetes がエンドポイントからこの Pod を
// 外すまでの猶予）。その後 srv.Shutdown を呼び、処理中のリクエストを最大 Timeout
// 待つ。どちらも 0（未設定）ならデフォルト値、負の値なら DrainDelay は待機なし。
type ShutdownConfig struct {
	DrainDelay time.Duration `mapstructure:"drainDelay"`
	Timeout    time.Duration `mapstructure:"timeout"`
}

// WithDefaults は未設定（0）の項目をデフォルト値で埋めたコピーを返す。
// DrainDelay の負の値は「待機しない」として 0 に正規化する。
func (c ShutdownConfig) WithDefaults() ShutdownConfig {
	switch {
	case c.DrainDelay == 0:
		c.DrainDelay = DefaultShutdownDrainDelay
	case c.DrainDelay < 0:
		c.DrainDelay = 0
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultShutdownTimeout
	}
	return c
}

func (c ShutdownConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c)
}
