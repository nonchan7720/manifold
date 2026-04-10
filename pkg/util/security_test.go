package util

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
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
		assert.Equal(t, tt.expected, SanitizeLog(tt.input))
	}
}

func TestValidatePath(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "base")
	err := os.MkdirAll(baseDir, 0755)
	assert.NoError(t, err)

	safeFile := filepath.Join(baseDir, "safe.txt")
	unsafeFile := filepath.Join(tmpDir, "unsafe.txt")

	_, err = ValidatePath(baseDir, safeFile)
	assert.NoError(t, err)

	_, err = ValidatePath(baseDir, unsafeFile)
	assert.Error(t, err)

	_, err = ValidatePath(baseDir, filepath.Join(baseDir, "..", "unsafe.txt"))
	assert.Error(t, err)
}

func TestIsAllowedDomain(t *testing.T) {
	allowed := []string{"example.com", "api.github.com"}

	tests := []struct {
		url     string
		allowed bool
	}{
		{"https://example.com/foo", true},
		{"https://api.github.com/v3", true},
		{"https://attacker.com/malicious", false},
		{"http://example.com:8080/path", true},
		{"invalid-url", false},
	}

	for _, tt := range tests {
		err := IsAllowedDomain(tt.url, allowed)
		if tt.allowed {
			assert.NoError(t, err, "URL %s should be allowed", tt.url)
		} else {
			assert.Error(t, err, "URL %s should be blocked", tt.url)
		}
	}
}
