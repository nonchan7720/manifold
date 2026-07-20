package config

import (
	"context"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type Storage struct {
	Type    string `mapstructure:"type"`
	HostURL string `mapstructure:"hostURL"`
	S3      *S3    `mapstructure:"s3"`
}

func (c *Storage) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, c,
		validation.Field(&c.Type, validation.In("", "s3")),
		validation.Field(&c.S3, validation.When(c.Type == "s3", validation.Required)),
	)
}

type S3 struct {
	Bucket    string `mapstructure:"bucket"`
	KeyPrefix string `mapstructure:"keyPrefix"`
}

func (c *S3) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, c,
		validation.Field(&c.Bucket, validation.Required),
		validation.Field(&c.KeyPrefix, validation.Required),
	)
}
