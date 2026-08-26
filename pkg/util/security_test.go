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
