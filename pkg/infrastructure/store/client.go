package store

import (
	"context"
	"time"
)

// Client はキーバリューストアの共通インターフェース。
// RedisやSQLiteなど複数のバックエンドで実装される。
type Client interface {
	// Set はキーに値をTTL付きで保存する。
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	// Get はキーに対応する値を文字列で返す。キーが存在しない場合はエラーを返す。
	Get(ctx context.Context, key string) (string, error)
	// Del はキーを削除する。
	Del(ctx context.Context, key string) error
	// Close はストアへの接続を閉じる。
	Close() error
}
