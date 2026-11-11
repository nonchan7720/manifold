package main

import (
	"testing"

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
	require.Equal(t, 8091, opts.port)
	require.Equal(t, keyValues{"X-Api-Key": "a=b"}, opts.downstreamHeaders)
	require.Equal(t, keyValues{"lang": "ja", "type": "{mime_type}"}, opts.openapiParams)
	require.Equal(t, "request", opts.extractedInputType)
	require.Empty(t, opts.fetchAllowedHosts)

	opts, err = parseFileFlags([]string{
		downstream, "--fetch-allowed-host", "files.example.com, *.cdn.example.com",
		"--fetch-allowed-host", "minio.internal:9000",
	})
	require.NoError(t, err)
	require.Equal(t,
		stringList{"files.example.com", "*.cdn.example.com", "minio.internal:9000"},
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
		{name: "overlap not smaller than chunk", args: []string{
			downstream, "--chunk-chars", "100", "--chunk-overlap-chars", "100",
		}, wantErr: "--chunk-overlap-chars must be smaller than --chunk-chars"},
		{name: "bad fetch size", args: []string{downstream, "--fetch-max-bytes", "0"},
			wantErr: "--fetch-max-bytes must be positive"},
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
