package util

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// SanitizeLog removes or escapes control characters like CRLF to prevent log injection.
func SanitizeLog(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// ValidatePath ensures that the given path is within the base directory.
// It returns the cleaned absolute path or an error if the path escapes the base.
func ValidatePath(baseDir, inputPath string) (string, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(inputPath)
	if err != nil {
		return "", err
	}

	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return "", err
	}

	if strings.HasPrefix(rel, "..") || rel == ".." {
		return "", fmt.Errorf("path escapes base directory: %s", inputPath)
	}

	return absPath, nil
}

// IsAllowedDomain checks if the given URL's domain is in the allowed list.
func IsAllowedDomain(inputURL string, allowedDomains []string) error {
	u, err := url.Parse(inputURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	hostname := u.Hostname()
	for _, domain := range allowedDomains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			return nil
		}
	}

	return fmt.Errorf("domain not allowed: %s", hostname)
}
