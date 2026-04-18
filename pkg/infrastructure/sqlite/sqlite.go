package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/n-creativesystem/go-packages/lib/trace"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS kv_store (
    key        TEXT    NOT NULL PRIMARY KEY,
    value      TEXT    NOT NULL,
    expires_at INTEGER NOT NULL
);
`

// Client はSQLiteを使ったキーバリューストアの実装。
// 外部サービス不要で動作する。
type Client struct {
	db *sql.DB
}

// NewClient は指定されたファイルパスにSQLiteデータベースを開き、Clientを返す。
// path に ":memory:" を指定するとインメモリDBとして動作する。
func NewClient(ctx context.Context, path string) (*Client, error) {
	db, err := otelsql.Open(
		"sqlite",
		path,
		otelsql.WithAttributes(semconv.DBSystemNameSQLite),
		otelsql.WithDBName(path),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// SQLiteはシングルライターなのでMaxOpenConnsを1に設定
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize sqlite schema: %w", err)
	}

	otelsql.ReportDBStatsMetrics(db)

	return &Client{db: db}, nil
}

// Set はキーに値をTTL付きで保存する。
func (c *Client) Set(ctx context.Context, key string, value any, expiration time.Duration) (rErr error) {
	ctx = trace.StartSpan(ctx, "sqlite/Client/Set")
	defer func() { trace.EndSpan(ctx, rErr) }()

	expiresAt := time.Now().Add(expiration).Unix()
	var strValue string
	switch v := value.(type) {
	case string:
		strValue = v
	case []byte:
		strValue = string(v)
	default:
		strValue = fmt.Sprintf("%v", v)
	}
	_, err := c.db.ExecContext(
		ctx,
		`INSERT INTO kv_store (key, value, expires_at) VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, expires_at = excluded.expires_at`,
		key, strValue, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("sqlite Set: %w", err)
	}
	return nil
}

// Get はキーに対応する値を返す。キーが存在しないか期限切れの場合はエラーを返す。
func (c *Client) Get(ctx context.Context, key string) (_ string, rErr error) {
	ctx = trace.StartSpan(ctx, "sqlite/Client/Get")
	defer func() { trace.EndSpan(ctx, rErr) }()

	var value string
	err := c.db.QueryRowContext(
		ctx,
		`SELECT value FROM kv_store WHERE key = ? AND expires_at > ?`,
		key, time.Now().Unix(),
	).Scan(&value)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("key not found: %s", key)
		}
		return "", fmt.Errorf("sqlite Get: %w", err)
	}
	return value, nil
}

// Del はキーを削除する。
func (c *Client) Del(ctx context.Context, key string) (rErr error) {
	ctx = trace.StartSpan(ctx, "sqlite/Client/Del")
	defer func() { trace.EndSpan(ctx, rErr) }()

	_, err := c.db.ExecContext(ctx, `DELETE FROM kv_store WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("sqlite Del: %w", err)
	}
	return nil
}

// DeleteExpired は期限切れのレコードをすべて削除する。
func (c *Client) DeleteExpired(ctx context.Context) error {
	_, err := c.db.ExecContext(ctx, `DELETE FROM kv_store WHERE expires_at <= ?`, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("sqlite DeleteExpired: %w", err)
	}
	return nil
}

// StartCleanup は定期的に期限切れレコードを削除するゴルーチンを起動する。
// ctxがキャンセルされると停止する。
func (c *Client) StartCleanup(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.DeleteExpired(ctx)
			}
		}
	}()
}

// Close はデータベース接続を閉じる。
func (c *Client) Close() error {
	return c.db.Close()
}
