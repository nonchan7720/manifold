package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "guardrail.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// 設定ファイルのキーはフラグの名前。リストとマップで書け、${VAR} は環境変数に置き換わる。
func TestParseFileFlags_ConfigFile(t *testing.T) {
	t.Setenv("DOWNSTREAM_API_KEY", "s3cret")
	path := writeConfig(t, `
host: 127.0.0.1
port: 9000
downstream-url: http://localhost:8090/beta/litellm_basic_guardrail_api
downstream-header:
  X-Api-Key: ${DOWNSTREAM_API_KEY}
  X-Tenant: ${TENANT:-default}
extractor: bedrock
model: some-model
extract-timeout: 90s
allow-unsupported-files: true
max-files: 8
fetch-allowed-host:
  - files.example.com
  - minio.internal:9000
`)
	opts, err := parseFileFlags([]string{"--config", path})
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", opts.host)
	require.Equal(t, 9000, opts.port)
	require.Equal(
		t,
		keyValues{"X-Api-Key": "s3cret", "X-Tenant": "default"},
		opts.downstreamHeaders,
	)
	require.Equal(t, "bedrock", opts.extractor)
	require.Equal(t, 90*time.Second, opts.extractTimeout)
	require.True(t, opts.allowUnsupported)
	require.Equal(t, 8, opts.maxFiles)
	require.Equal(t, stringList{"files.example.com", "minio.internal:9000"}, opts.fetchAllowedHosts)
	// ファイルに無い設定は既定値のまま。
	require.Equal(t, 4, opts.concurrency)

	// コマンドラインのフラグはファイルより優先する。繰り返せるフラグは置き換える。
	opts, err = parseFileFlags([]string{
		"--config", path, "--port", "9100", "--fetch-allowed-host", "cdn.example.com",
	})
	require.NoError(t, err)
	require.Equal(t, 9100, opts.port)
	require.Equal(t, stringList{"cdn.example.com"}, opts.fetchAllowedHosts)

	// 環境変数でもファイルを指定できる。
	t.Setenv("FILE_GUARDRAIL_CONFIG", path)
	opts, err = parseFileFlags(nil)
	require.NoError(t, err)
	require.Equal(t, 9000, opts.port)
}

func TestParseFileFlags_ConfigFileErrors(t *testing.T) {
	const downstream = "downstream-url: http://localhost:8090/x\n"
	for _, tc := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "unknown setting", body: downstream + "fetch-allowed-hosts: [a]\n",
			wantErr: `unknown setting "fetch-allowed-hosts"`},
		{name: "config in config", body: downstream + "config: other.yaml\n",
			wantErr: `unknown setting "config"`},
		{name: "list for a scalar", body: downstream + "port: [1, 2]\n",
			wantErr: "port: does not take a list"},
		{name: "map for a scalar", body: downstream + "model: {a: b}\n",
			wantErr: "model: does not take a map"},
		{name: "bad value", body: downstream + "port: abc\n", wantErr: "port: invalid value"},
		{name: "unset variable", body: "downstream-url: ${FILE_GUARDRAIL_TEST_UNSET}\n",
			wantErr: "environment variable FILE_GUARDRAIL_TEST_UNSET is not set"},
		{name: "not yaml", body: "port: [", wantErr: "config:"},
		// ファイルの値も、フラグと同じ検証を通る。
		{name: "validated", body: downstream + "max-files: 0\n",
			wantErr: "--max-files must be positive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFileFlags([]string{"--config", writeConfig(t, tc.body)})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
	_, err := parseFileFlags([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")})
	require.ErrorContains(t, err, "config:")
}

// 値は秘匿値を含みうるので、不正な値のエラーに値そのものを出さない。
func TestParseFileFlags_ConfigErrorOmitsValue(t *testing.T) {
	path := writeConfig(
		t,
		"downstream-url: http://localhost:8090/x\nextract-timeout: s3cret-value\n",
	)
	_, err := parseFileFlags([]string{"--config", path})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "s3cret-value")
}
