package config

import (
	"context"
	"fmt"
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/nonchan7720/manifold/pkg/internal/oasbreaking"
)

// SpecRefreshConfig is the gateway-wide default for re-fetching OpenAPI mode
// specs after startup, overridable per server with
// mcpServers.<name>.specRefreshInterval. Interval 0 (unset) disables refreshing.
//
// RejectOn is the oasdiff level (ERR, WARN, INFO) at or above which a
// refreshed spec is rejected and the previous tools kept serving,
// overridable per server with mcpServers.<name>.specRefreshRejectOn.
// "" (unset) or NONE never rejects.
type SpecRefreshConfig struct {
	Interval time.Duration `mapstructure:"interval"`
	RejectOn string        `mapstructure:"rejectOn"`
}

func (c SpecRefreshConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &c,
		validation.Field(&c.Interval, validation.By(func(value any) error {
			if c.Interval < 0 {
				return fmt.Errorf("must be zero or a positive duration")
			}
			return nil
		})),
		validation.Field(&c.RejectOn, validation.By(validateRejectOn)),
	)
}

// validateRejectOn validates a rejectOn level as oasbreaking.ParseLevel does.
func validateRejectOn(value any) error {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case *string:
		if v == nil {
			return nil
		}
		s = *v
	}
	_, err := oasbreaking.ParseLevel(s)
	return err
}
