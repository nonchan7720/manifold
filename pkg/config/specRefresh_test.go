package config

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestSpecRefreshConfig_ValidateWithContext_Zero_Valid(t *testing.T) {
	c := SpecRefreshConfig{}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestSpecRefreshConfig_ValidateWithContext_Positive_Valid(t *testing.T) {
	c := SpecRefreshConfig{Interval: 5 * time.Minute}
	require.NoError(t, c.ValidateWithContext(t.Context()))
}

func TestSpecRefreshConfig_ValidateWithContext_Negative_Invalid(t *testing.T) {
	c := SpecRefreshConfig{Interval: -1 * time.Second}
	require.Error(t, c.ValidateWithContext(t.Context()))
}

func TestGateway_ValidateWithContext_SpecRefreshInterval_Negative_Invalid(t *testing.T) {
	g := Gateway{
		EncryptKey:  validEncryptKey,
		SpecRefresh: SpecRefreshConfig{Interval: -1 * time.Second},
	}
	err := g.ValidateWithContext(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "Interval")
}

func TestGateway_ValidateWithContext_SpecRefreshInterval_Positive_Valid(t *testing.T) {
	g := Gateway{
		EncryptKey:  validEncryptKey,
		SpecRefresh: SpecRefreshConfig{Interval: 10 * time.Minute},
	}
	require.NoError(t, g.ValidateWithContext(t.Context()))
}

func TestSpecRefreshConfig_UnmarshalFromYAML(t *testing.T) {
	const yaml = `
gateway:
  specRefresh:
    interval: 5m
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yaml)))

	var conf Config
	require.NoError(t, v.Unmarshal(&conf))
	require.Equal(t, 5*time.Minute, conf.Gateway.SpecRefresh.Interval)
}

func TestSpecRefreshConfig_UnmarshalFromYAML_Unset(t *testing.T) {
	const yaml = `
gateway:
  port: 9999
`
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yaml)))

	var conf Config
	require.NoError(t, v.Unmarshal(&conf))
	require.Zero(t, conf.Gateway.SpecRefresh.Interval)
}
