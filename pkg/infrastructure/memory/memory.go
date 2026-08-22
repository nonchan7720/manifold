package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
)

type entry struct {
	value     string
	expiresAt time.Time
}

// Client はプロセス内メモリを使ったキーバリューストアの実装。
// 外部サービス不要で動作するが、プロセス終了とともにデータは失われる。
type Client struct {
	mu   sync.Mutex
	data map[string]entry
}

// NewClient はインメモリのClientを生成する。
func NewClient(_ context.Context) (*Client, error) {
	return &Client{
		data: make(map[string]entry),
	}, nil
}

// Set はキーに値をTTL付きで保存する。
func (c *Client) Set(_ context.Context, key string, value any, expiration time.Duration) error {
	var strValue string
	switch v := value.(type) {
	case string:
		strValue = v
	case []byte:
		strValue = string(v)
	default:
		strValue = fmt.Sprintf("%v", v)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = entry{value: strValue, expiresAt: time.Now().Add(expiration)}
	return nil
}

// Get はキーに対応する値を返す。キーが存在しないか期限切れの場合はエラーを返す。
func (c *Client) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.data[key]
	if !ok {
		return "", fmt.Errorf("key not found: %s: %w", key, store.ErrNotFound)
	}
	if time.Now().After(e.expiresAt) {
		delete(c.data, key)
		return "", fmt.Errorf("key not found: %s: %w", key, store.ErrNotFound)
	}
	return e.value, nil
}

// Expire はキーの値を変更せずTTLだけを更新する。キーが存在しないか期限切れの場合はエラーを返す。
func (c *Client) Expire(_ context.Context, key string, expiration time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.data[key]
	if !ok || time.Now().After(e.expiresAt) {
		delete(c.data, key)
		return fmt.Errorf("key not found: %s", key)
	}
	e.expiresAt = time.Now().Add(expiration)
	c.data[key] = e
	return nil
}

// Del はキーを削除する。
func (c *Client) Del(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	return nil
}

// Close は何もせずnilを返す。
func (c *Client) Close() error {
	return nil
}
