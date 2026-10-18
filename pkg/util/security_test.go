package util

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeLog(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello\nworld", "helloworld"},
		{"hello\rworld", "helloworld"},
		{"hello\r\nworld", "helloworld"},
		{"clean", "clean"},
	}

	for _, tt := range tests {
		require.Equal(t, tt.expected, SanitizeLog(tt.input))
	}
}

func TestRedactRequestURI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"no query", "/browser/auth/login", "/browser/auth/login"},
		{"empty query", "/browser/auth/login?", "/browser/auth/login?"},
		{
			"callback code and state",
			"/browser/auth/callback?code=secret-code&state=secret-state",
			"/browser/auth/callback?code=REDACTED&state=REDACTED",
		},
		{
			"keeps non-sensitive params",
			"/auth/login?response_type=code&client_id=abc&code_challenge=xyz&code_challenge_method=S256&state=s",
			"/auth/login?client_id=abc&code_challenge=REDACTED&code_challenge_method=S256&response_type=code&state=REDACTED",
		},
		{"case insensitive", "/x?Access_Token=t", "/x?Access_Token=REDACTED"},
		{"unparsable query", "/x?code=%zz", "/x?REDACTED"},
		{"crlf", "/x\r\n?refresh_token=t", "/x?refresh_token=REDACTED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, RedactRequestURI(tt.input))
		})
	}
}

func TestFingerprint(t *testing.T) {
	require.Empty(t, Fingerprint(""))
	fp := Fingerprint("secret")
	require.Len(t, fp, 12)
	require.NotContains(t, fp, "secret")
	require.Equal(t, fp, Fingerprint("secret"))
	require.NotEqual(t, fp, Fingerprint("other"))
}
