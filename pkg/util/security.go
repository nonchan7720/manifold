package util

import (
	"strings"
)

// SanitizeLog removes or escapes control characters like CRLF to prevent log injection.
func SanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}
