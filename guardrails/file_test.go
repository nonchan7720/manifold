package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseFileFlags(t *testing.T) {
	t.Setenv("FILE_GUARDRAIL_DOWNSTREAM_URL", "")
	t.Setenv("FILE_GUARDRAIL_MODEL", "")
	t.Setenv("AZURE_OPENAI_API_KEY", "")
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")
	const downstream = "--downstream-url=http://localhost:8090/beta/litellm_basic_guardrail_api"

	opts, err := parseFileFlags([]string{
		downstream, "--downstream-header", "X-Api-Key=a=b", "--extractor", "openapi",
		"--openapi-spec", "spec.yaml", "--openapi-operation", "ocr",
		"--openapi-param", "lang=ja", "--openapi-param", "type={mime_type}",
	})
	require.NoError(t, err)
	require.Empty(t, opts.host)
	require.Equal(t, 8091, opts.port)
	require.Equal(t, keyValues{"X-Api-Key": "a=b"}, opts.downstreamHeaders)
	require.Equal(t, keyValues{"lang": "ja", "type": "{mime_type}"}, opts.openapiParams)
	require.Equal(t, "request", opts.extractedInputType)
	require.Empty(t, opts.fetchAllowedHosts)

	opts, err = parseFileFlags([]string{
		downstream, "--fetch-allowed-host", "files.example.com, cdn.example.com",
		"--fetch-allowed-host", "minio.internal:9000",
	})
	require.NoError(t, err)
	require.Equal(t,
		stringList{"files.example.com", "cdn.example.com", "minio.internal:9000"},
		opts.fetchAllowedHosts)

	for _, tc := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "missing downstream", args: nil, wantErr: "--downstream-url is required"},
		{name: "bad header", args: []string{downstream, "--downstream-header", "novalue"},
			wantErr: "must be name=value"},
		{name: "bad input type", args: []string{downstream, "--extracted-input-type", "x"},
			wantErr: "--extracted-input-type"},
		{name: "unknown extractor", args: []string{downstream, "--extractor", "tesseract"},
			wantErr: `unknown --extractor "tesseract"`},
		{name: "bedrock without model", args: []string{downstream, "--extractor", "bedrock"},
			wantErr: "--model is required"},
		{name: "azure without key", args: []string{
			downstream, "--extractor", "azure", "--model", "d", "--azure-endpoint", "https://x",
		}, wantErr: "AZURE_OPENAI_API_KEY is required"},
		{name: "openapi without spec", args: []string{downstream, "--extractor", "openapi"},
			wantErr: "--openapi-spec and --openapi-operation are required"},
		// 重なりが断片の半分を超えると、分割後の合計が何倍にも膨らむ。
		{name: "overlap over half of chunk", args: []string{
			downstream, "--chunk-chars", "201", "--chunk-overlap-chars", "200",
		}, wantErr: "--chunk-overlap-chars must be at most half of --chunk-chars"},
		{name: "chunking without overlap", args: []string{
			downstream, "--chunk-chars", "100", "--chunk-overlap-chars", "0",
		}, wantErr: "--chunk-overlap-chars must be positive when --chunk-chars is set"},
		{name: "bad max chunks", args: []string{downstream, "--max-chunks", "0"},
			wantErr: "--max-chunks must be positive"},
		{name: "bad fetch size", args: []string{downstream, "--fetch-max-bytes", "0"},
			wantErr: "--fetch-max-bytes must be positive"},
		{name: "downstream not a url", args: []string{"--downstream-url", "not-a-url"},
			wantErr: "--downstream-url must be an absolute http(s) URL"},
		{name: "downstream without host", args: []string{"--downstream-url", "http:///x"},
			wantErr: "--downstream-url must be an absolute http(s) URL"},
		{name: "downstream with a port only", args: []string{"--downstream-url", "http://:80/x"},
			wantErr: "--downstream-url must be an absolute http(s) URL"},
		{name: "bad extract timeout", args: []string{downstream, "--extract-timeout", "0s"},
			wantErr: "--extract-timeout must be positive"},
		{name: "bad fetch timeout", args: []string{downstream, "--fetch-timeout", "-1s"},
			wantErr: "--fetch-timeout must be positive"},
		{name: "bad concurrency", args: []string{downstream, "--concurrency", "0"},
			wantErr: "--concurrency must be positive"},
		{name: "bad max files", args: []string{downstream, "--max-files", "0"},
			wantErr: "--max-files must be positive"},
		{name: "stray argument", args: []string{downstream, "extra"}, wantErr: "unexpected argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFileFlags(tc.args)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	require.ErrorContains(t, run(t.Context(), []string{"bedrock"}), `unknown command "bedrock"`)
	require.ErrorContains(t, run(t.Context(), nil), "a command is required")
}

// 応答を書き終えるまでの上限は、--max-files 件を --concurrency 件ずつ取得して起こし、
// そのあと下流を呼ぶ、最も遅い場合より長い。
func TestFileOptions_WriteTimeout(t *testing.T) {
	opts, err := parseFileFlags([]string{
		"--downstream-url=http://localhost:8090/beta/litellm_basic_guardrail_api",
		"--max-files", "17", "--concurrency", "4",
		"--extract-timeout", "120s", "--fetch-timeout", "30s",
	})
	require.NoError(t, err)
	// 17 件は 5 回に分かれる: 5 * (30s + 120s) + 下流 120s + 余裕 1m。
	require.Equal(t, 5*150*time.Second+120*time.Second+time.Minute, opts.writeTimeout())
}
