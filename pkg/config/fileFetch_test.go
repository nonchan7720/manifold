package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileFetchConfig_Validate_DeniedCIDRs(t *testing.T) {
	ok := FileFetchConfig{
		DeniedCIDRs: []string{"10.0.0.0/8", "fd00::/8"},
		DeniedHosts: []string{"vault.internal"},
	}
	require.NoError(t, ok.ValidateWithContext(t.Context()))

	bad := FileFetchConfig{DeniedCIDRs: []string{"10.0.0.0/8", "10.0.0.1"}}
	err := bad.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be a valid CIDR")
}
