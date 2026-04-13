package config

import (
	"context"
	"encoding/base64"
	"fmt"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type Config struct {
	Gateway   Gateway `mapstructure:"gateway"`
	MCPServer Servers `mapstructure:"mcpServers"`

	Redis  *RedisConfig  `mapstructure:"redis"`
	SQLite *SQLiteConfig `mapstructure:"sqlite"`
}

func (c *Config) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(
		ctx,
		c,
		validation.Field(&c.Gateway),
		validation.Field(&c.MCPServer),
		validation.Field(&c.Redis, validation.When(c.SQLite == nil, validation.Required)),
		validation.Field(&c.SQLite, validation.When(c.SQLite == nil, validation.Required)),
	)
}

type Gateway struct {
	Port int `mapstructure:"port"`

	Key  string `mapstructure:"key"`
	Cert string `mapstructure:"cert"`

	EncryptKey string `mapstructure:"encryptKey"`
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
	)
}
