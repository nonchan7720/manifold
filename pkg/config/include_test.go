package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestLoadWithIncludes_MergesGatewayFromOtherFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include:
  - gateway.yaml
sqlite:
  path: ./tmp/manifold.db
`)
	writeFile(t, filepath.Join(dir, "gateway.yaml"), `
gateway:
  port: 9000
  encryptKey: abc
`)

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"gateway": map[string]any{"port": 9000, "encryptKey": "abc"},
		"sqlite":  map[string]any{"path": "./tmp/manifold.db"},
	}, got)
}

func TestLoadWithIncludes_IncludingFileWinsAndMapsDeepMerge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include: [base.yaml, override.yaml]
gateway:
  port: 1
`)
	writeFile(t, filepath.Join(dir, "base.yaml"), `
gateway:
  port: 9000
  encryptKey: base
  edge:
    enabled: true
`)
	writeFile(t, filepath.Join(dir, "override.yaml"), `
Gateway:
  encryptKey: override
`)

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"gateway": map[string]any{
			"port":       1,
			"encryptKey": "override",
			"edge":       map[string]any{"enabled": true},
		},
	}, got)
}

func TestLoadWithIncludes_GlobAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MANIFOLD_CONF_DIR", "conf.d")
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include: ${MANIFOLD_CONF_DIR}/*.yaml
`)
	writeFile(t, filepath.Join(dir, "conf.d", "10-gateway.yaml"), `
gateway: {port: 1, encryptKey: k}
`)
	writeFile(t, filepath.Join(dir, "conf.d", "20-port.yaml"), `
gateway: {port: 2}
`)

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"gateway": map[string]any{"port": 2, "encryptKey": "k"},
	}, got)
}

func TestLoadWithIncludes_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: [nope.yaml]\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, "nope.yaml")
	})
	t.Run("key other than gateway", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: [a.yaml]\n")
		writeFile(t, filepath.Join(dir, "a.yaml"), "mcpServers:\n  a: {url: https://a}\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, `key "mcpServers" is not allowed`)
	})
	t.Run("nested include", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: [a.yaml]\n")
		writeFile(t, filepath.Join(dir, "a.yaml"), "include: [b.yaml]\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, `key "include" is not allowed`)
	})
	t.Run("invalid type", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: {a: b}\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, "include must be")
	})
}

func TestLoadInternal_IncludeGateway(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifold-include-test.yaml"), `
include: [gateway.yaml]
sqlite:
  path: ./tmp/manifold.db
`)
	writeFile(t, filepath.Join(dir, "gateway.yaml"), `
gateway:
  port: 7777
  encryptKey: ${TEST_INCLUDE_ENCRYPT_KEY}
`)
	t.Setenv("TEST_INCLUDE_ENCRYPT_KEY", "5pJXItSsvVwbxS4gysMYf5Zn1z5dYP6uCQn2xVAqtlM=")
	t.Chdir(dir)

	cfg, err := loadInternal(t.Context(), "manifold-include-test")
	require.NoError(t, err)
	require.Equal(t, 7777, cfg.Gateway.Port)
	require.Equal(t, "5pJXItSsvVwbxS4gysMYf5Zn1z5dYP6uCQn2xVAqtlM=", cfg.Gateway.EncryptKey)
	require.NotNil(t, cfg.SQLite)
}
