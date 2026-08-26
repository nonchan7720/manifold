package config

import (
	"context"
	"fmt"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// SpecRefreshConfig is the gateway-wide default for re-fetching OpenAPI mode
// specs after startup, overridable per server with
// mcpServers.<name>.specRefreshInterval. Interval 0 (unset) disables refreshing.
type SpecRefreshConfig struct {
	Interval time.Duration `mapstructure:"interval"`
}

func (c SpecRefreshConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Interval, validation.By(func(value any) error {
			if c.Interval < 0 {
				return fmt.Errorf("must be zero or a positive duration")
			}
			return nil
		})),
	)
}
