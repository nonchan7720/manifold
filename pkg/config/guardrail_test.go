package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGuardrailConfig_Validate(t *testing.T) {
	const url = "http://localhost:8090/beta/litellm_basic_guardrail_api"
	for _, tc := range []struct {
		name    string
		cfg     GuardrailConfig
		wantErr string
	}{
		{name: "disabled ignores everything", cfg: GuardrailConfig{URL: "nope"}},
		{name: "valid", cfg: GuardrailConfig{
			Enabled: true, URL: url, Headers: map[string]string{"X-Api-Key": "k"},
			Mode: []string{GuardrailModePreCall},
		}},
		{name: "missing url", cfg: GuardrailConfig{Enabled: true}, wantErr: "URL: cannot be blank"},
		{name: "non-http url", cfg: GuardrailConfig{Enabled: true, URL: "unix:///g.sock"},
			wantErr: "must use http or https"},
		{name: "bad header", cfg: GuardrailConfig{
			Enabled: true, URL: url, Headers: map[string]string{"bad header": "x"},
		}, wantErr: "must be a valid HTTP header field name"},
		{name: "unknown mode", cfg: GuardrailConfig{Enabled: true, URL: url, Mode: []string{"during_call"}},
			wantErr: "must be a valid value"},
		{name: "negative timeout", cfg: GuardrailConfig{Enabled: true, URL: url, Timeout: -time.Second},
			wantErr: "must be no less than 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.ValidateWithContext(t.Context())
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestGuardrailConfig_Mode(t *testing.T) {
	both := GuardrailConfig{}
	require.True(t, both.PreCall())
	require.True(t, both.PostCall())
	pre := GuardrailConfig{Mode: []string{GuardrailModePreCall}}
	require.True(t, pre.PreCall())
	require.False(t, pre.PostCall())
	post := GuardrailConfig{Mode: []string{GuardrailModePostCall}}
	require.False(t, post.PreCall())
	require.True(t, post.PostCall())
	require.Equal(t, DefaultGuardrailTimeout, both.TimeoutOrDefault())
}
