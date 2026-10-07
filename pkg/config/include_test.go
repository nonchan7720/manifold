package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeFile writes content to path, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestLoadWithIncludes_MergesMCPServersFromServiceFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include:
  - serviceA.yaml
  - serviceB.yaml
gateway:
  port: 9000
`)
	writeFile(t, filepath.Join(dir, "serviceA.yaml"), `
mcpServers:
  xxx:
    url: https://xxx
`)
	writeFile(t, filepath.Join(dir, "serviceB.yaml"), `
mcpServers:
  yyy:
    url: https://yyy
`)

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"gateway": map[string]any{"port": uint64(9000)},
		"mcpServers": map[string]any{
			"xxx": map[string]any{"url": "https://xxx"},
			"yyy": map[string]any{"url": "https://yyy"},
		},
	}, got)
}

func TestLoadWithIncludes_IncludingFileWinsAndMapsDeepMerge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include: [base.yaml, override.yaml]
mcpServers:
  a:
    description: from main
`)
	writeFile(t, filepath.Join(dir, "base.yaml"), `
mcpServers:
  a:
    url: https://a
    description: from base
    headers: {X-Base: "1"}
  b:
    url: https://b
`)
	writeFile(t, filepath.Join(dir, "override.yaml"), `
MCPServers:
  a:
    url: https://a2
  c:
    url: https://c
`)

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"mcpServers": map[string]any{
			"a": map[string]any{
				"url":         "https://a2",
				"description": "from main",
				"headers":     map[string]any{"X-Base": "1"},
			},
			"b": map[string]any{"url": "https://b"},
			"c": map[string]any{"url": "https://c"},
		},
	}, got)
}

func TestLoadWithIncludes_GlobAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MANIFOLD_CONF_DIR", "mcp-servers.d")
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include: ${MANIFOLD_CONF_DIR}/*.yaml
`)
	writeFile(t, filepath.Join(dir, "mcp-servers.d", "10-a.yaml"), `
mcpServers: {a: {url: https://a1}}
`)
	writeFile(t, filepath.Join(dir, "mcp-servers.d", "20-a.yaml"), `
mcpServers: {a: {url: https://a2}, b: {url: https://b}}
`)

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"mcpServers": map[string]any{
			"a": map[string]any{"url": "https://a2"},
			"b": map[string]any{"url": "https://b"},
		},
	}, got)
}

// A Kubernetes ConfigMap mount looks like this:
//
//	services/
//	  ..2026_09_28_15_06_52.1338942298/   (directory holding the real files)
//	  ..data -> ..2026_09_28_15_06_52.1338942298
//	  a.yaml -> ..data/a.yaml
//
// A glob such as "services/*" matches the directory and the symlink to it,
// which must be skipped instead of being read as files.
func TestLoadWithIncludes_GlobSkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), `
include: services/*
`)
	services := filepath.Join(dir, "services")
	versioned := filepath.Join(services, "..2026_09_28_15_06_52.1338942298")
	writeFile(t, filepath.Join(versioned, "a.yaml"), `
mcpServers: {a: {url: https://a}}
`)
	require.NoError(t, os.Symlink(versioned, filepath.Join(services, "..data")))
	require.NoError(t, os.Symlink(
		filepath.Join("..data", "a.yaml"), filepath.Join(services, "a.yaml"),
	))

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"mcpServers": map[string]any{
			"a": map[string]any{"url": "https://a"},
		},
	}, got)
}

func TestLoadWithIncludes_IncludeKeyIsCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "Include: [serviceA.yaml]\n")
	writeFile(t, filepath.Join(dir, "serviceA.yaml"), "mcpServers: {xxx: {url: https://xxx}}\n")

	got, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"mcpServers": map[string]any{"xxx": map[string]any{"url": "https://xxx"}},
	}, got)
}

func TestLoadWithIncludes_Errors(t *testing.T) {
	t.Run("include key set twice with different casing", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: [a.yaml]\nInclude: [b.yaml]\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, "more than once")
	})
	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: [nope.yaml]\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, "nope.yaml")
	})
	t.Run("key other than mcpServers", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "config.yaml"), "include: [a.yaml]\n")
		writeFile(t, filepath.Join(dir, "a.yaml"), "gateway:\n  port: 1\n")
		_, err := loadWithIncludes(filepath.Join(dir, "config.yaml"))
		require.ErrorContains(t, err, `key "gateway" is not allowed`)
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

func TestLoadInternal_IncludeMCPServers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "manifold-include-test.yaml"), `
include: [mcp-servers.yaml]
gateway:
  port: 7777
  encryptKey: ${TEST_INCLUDE_ENCRYPT_KEY}
sqlite:
  path: ./tmp/manifold.db
`)
	writeFile(t, filepath.Join(dir, "mcp-servers.yaml"), `
mcpServers:
  notion:
    transport: http
    url: ${TEST_INCLUDE_NOTION_URL}
    description: notion
`)
	t.Setenv("TEST_INCLUDE_ENCRYPT_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("TEST_INCLUDE_NOTION_URL", "https://mcp.notion.com/mcp")
	t.Chdir(dir)

	cfg, err := loadInternal(t.Context(), "manifold-include-test")
	require.NoError(t, err)
	require.Equal(t, 7777, cfg.Gateway.Port)
	require.Contains(t, cfg.MCPServer, "notion")
	require.Equal(t, "notion", cfg.MCPServer["notion"].Name)
	require.Equal(t, "https://mcp.notion.com/mcp", cfg.MCPServer["notion"].URL)
}
