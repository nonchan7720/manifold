package config

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/nonchan7720/manifold/pkg/internal/telemetry"
)

type Config struct {
	Gateway   Gateway `mapstructure:"gateway"`
	MCPServer Servers `mapstructure:"mcpServers"`

	Redis  *RedisConfig  `mapstructure:"redis"`
	SQLite *SQLiteConfig `mapstructure:"sqlite"`
	Memory *MemoryConfig `mapstructure:"memory"`

	Telemetry telemetry.Config `mapstructure:"telemetry"`

	FileFetch FileFetchConfig `mapstructure:"fileFetch"`

	Storage Storage `mapstructure:"storage"`
}

// URL パスセグメントとして使われるサーバー名として妥当な文字集合。
// ドットは除外しつつ、既存設定で使われてきたハイフン区切りの名前を壊さないようハイフンを許可する。
var pathRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (c *Config) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		c,
		validation.Field(&c.Gateway),
		validation.Field(&c.MCPServer, validation.By(func(value any) error {
			mp, ok := value.(Servers)
			if !ok {
				return fmt.Errorf("type error: %T", value)
			}
			for key := range mp {
				if !pathRegex.MatchString(key) {
					return fmt.Errorf("key '%s' contains invalid characters", key)
				}
			}
			return nil
		})),
		validation.Field(
			&c.Redis,
			validation.When(c.SQLite == nil && c.Memory == nil, validation.Required),
		),
		validation.Field(
			&c.SQLite,
			validation.When(c.Redis == nil && c.Memory == nil, validation.Required),
		),
		validation.Field(&c.Storage),
	)
}

type Gateway struct {
	Port int `mapstructure:"port"`

	Key  string `mapstructure:"key"`
	Cert string `mapstructure:"cert"`

	EncryptKey string `mapstructure:"encryptKey"`

	// BaseURL は外部公開時の正規ベース URL（例: https://gateway.example.com）。
	// 設定すると OAuth の audience 検証やメタデータ生成にこの値が使われ、
	// クライアント制御の Host ヘッダーに依存しなくなる。
	BaseURL string `mapstructure:"baseURL"`
}

func (c Gateway) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&c,
		validation.Field(&c.BaseURL,
			validation.When(c.BaseURL != "",
				validation.By(func(value any) error {
					v, ok := value.(string)
					if !ok {
						return fmt.Errorf("must be string type")
					}
					u, err := url.Parse(v)
					if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
						return fmt.Errorf("must be an absolute http(s) URL")
					}
					// resource の audience 検証は /mcp/{name} のパス構造を前提と
					// するため、パスプレフィックス付きの公開はサポートしない
					if strings.Trim(u.Path, "/") != "" || u.ForceQuery || u.RawQuery != "" ||
						u.Fragment != "" {
						return fmt.Errorf("must not contain a path, query, or fragment")
					}
					return nil
				}),
			),
		),
		validation.Field(&c.EncryptKey,
			validation.Required,
			validation.When(c.EncryptKey != "",
				validation.By(func(value any) error {
					v, ok := value.(string)
					if !ok {
						return fmt.Errorf("must be string type")
					}
					decoded, err := base64.StdEncoding.DecodeString(v)
					// AES-256 requires 32 bytes key
					if len(decoded) != 32 {
						return fmt.Errorf("key must be 32 bytes for AES-256")
					}
					return err
				}),
			),
		),
	)
}
