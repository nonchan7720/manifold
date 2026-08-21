package config

import (
	"context"
	"encoding/base64"
	"fmt"
	"regexp"

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

// edgeContextKey carries the effective Gateway.Edge config into Server
// validation, so a reverse Server's identity requirement can depend on
// whether pairing.type is static without Server needing a Gateway reference.
type edgeContextKey struct{}

func (c *Config) ValidateWithContext(ctx context.Context) error {
	ctx = context.WithValue(ctx, edgeContextKey{}, c.Gateway.Edge.WithDefaults())
	return validation.ValidateStructWithContext(
		ctx,
		c,
		validation.Field(&c.Gateway),
		validation.Field(&c.MCPServer, validation.By(func(value any) error {
			mp, ok := value.(Servers)
			if !ok {
				return fmt.Errorf("type error: %T", value)
			}
			origins := map[string]string{}
			for key, srv := range mp {
				if !pathRegex.MatchString(key) {
					return fmt.Errorf("key '%s' contains invalid characters", key)
				}
				if srv.Transport != MCPTransportReverse {
					continue
				}
				normalized, err := NormalizeOrigin(srv.Origin)
				if err != nil {
					continue // Server.ValidateWithContext がこの不正値自体を報告する
				}
				if other, dup := origins[normalized]; dup {
					return fmt.Errorf(
						"origin %q is used by both server '%s' and '%s'; origins must be unique",
						normalized, other, key,
					)
				}
				origins[normalized] = key
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

	Edge EdgeConfig `mapstructure:"edge"`
}

func (c Gateway) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		&c,
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
		validation.Field(&c.Edge),
	)
}
