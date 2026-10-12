package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditConfig_ValidateRedact(t *testing.T) {
	for _, tc := range []struct {
		name    string
		redact  AuditRedactConfig
		wantErr string
	}{
		{name: "empty"},
		{name: "valid", redact: AuditRedactConfig{
			Keys: []string{"ssn"}, Patterns: []string{`AKIA[0-9A-Z]{16}`},
		}},
		{name: "invalid pattern", redact: AuditRedactConfig{Patterns: []string{"("}},
			wantErr: "invalid regular expression"},
		{name: "empty pattern", redact: AuditRedactConfig{Patterns: []string{""}},
			wantErr: "cannot be blank"},
		{name: "empty key", redact: AuditRedactConfig{Keys: []string{""}},
			wantErr: "cannot be blank"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := AuditConfig{Redact: tc.redact}.ValidateWithContext(t.Context())
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
