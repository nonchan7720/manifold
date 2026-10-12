package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAuditConfig_ValidateGuardrail(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     AuditConfig
		wantErr string
	}{
		{
			name: "valid",
			cfg: AuditConfig{IncludeArguments: true, Guardrail: &AuditGuardrailConfig{
				URL:     "http://localhost:8090/beta/litellm_basic_guardrail_api",
				Headers: map[string]string{"X-Api-Key": "secret"},
			}},
		},
		{
			name: "without includeArguments",
			cfg: AuditConfig{Guardrail: &AuditGuardrailConfig{
				URL: "http://localhost:8090/beta/litellm_basic_guardrail_api",
			}},
			wantErr: "requires includeArguments: true",
		},
		{
			name:    "missing url",
			cfg:     AuditConfig{IncludeArguments: true, Guardrail: &AuditGuardrailConfig{}},
			wantErr: "URL: cannot be blank",
		},
		{
			name: "non-http url",
			cfg: AuditConfig{IncludeArguments: true, Guardrail: &AuditGuardrailConfig{
				URL: "unix:///tmp/guardrail.sock",
			}},
			wantErr: "must use http or https",
		},
		{
			name: "bad header",
			cfg: AuditConfig{IncludeArguments: true, Guardrail: &AuditGuardrailConfig{
				URL: "http://localhost:8090", Headers: map[string]string{"bad header": "x"},
			}},
			wantErr: "must be a valid HTTP header field name",
		},
		{
			name: "negative values",
			cfg: AuditConfig{IncludeArguments: true, Guardrail: &AuditGuardrailConfig{
				URL: "http://localhost:8090", Timeout: -time.Second, QueueSize: -1, Concurrency: -1,
			}},
			wantErr: "must be no less than 0",
		},
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

func TestAuditGuardrailConfig_Defaults(t *testing.T) {
	var c AuditGuardrailConfig
	require.Equal(t, DefaultAuditGuardrailTimeout, c.TimeoutOrDefault())
	require.Equal(t, DefaultAuditGuardrailQueueSize, c.QueueSizeOrDefault())
	require.Equal(t, DefaultAuditGuardrailConcurrency, c.ConcurrencyOrDefault())
}
