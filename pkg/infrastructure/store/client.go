package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound はGetがキー不存在(または期限切れ)を報告するときのsentinel error。
// 呼び出し元はerrors.Is(err, ErrNotFound)で、キー不存在と予期しないバックエンド
// 障害を区別できる。
var ErrNotFound = errors.New("store: key not found")

// Client はキーバリューストアの共通インターフェース。
// RedisやSQLiteなど複数のバックエンドで実装される。
type Client interface {
	// Set はキーに値をTTL付きで保存する。
	Set(ctx context.Context, key string, value any, expiration time.Duration) error
	// Get はキーに対応する値を文字列で返す。キーが存在しない場合はErrNotFoundを
	// ラップしたエラーを返す。
	Get(ctx context.Context, key string) (string, error)
	// Expire はキーの値を変更せずTTLだけを更新する。キーが存在しない場合はエラーを返す。
	Expire(ctx context.Context, key string, expiration time.Duration) error
	// Del はキーを削除する。
	Del(ctx context.Context, key string) error
	// Close はストアへの接続を閉じる。
	Close() error
}
