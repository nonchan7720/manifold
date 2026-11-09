package cmd

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// petstoreSpecYAML is petstoreSpecJSON's first two operations as YAML, with
// an absolute servers entry.
const petstoreSpecYAML = `openapi: 3.0.3
info:
  title: Petstore
  version: 1.0.0
servers:
  - url: https://petstore.example.com/v1
paths:
  /pet:
    post:
      operationId: addPet
      summary: Add a new pet to the store
      responses:
        "200": {description: ok}
  /pet/{petId}:
    get:
      operationId: getPetById
      summary: Find pet by ID
      parameters:
        - {name: petId, in: path, required: true, schema: {type: integer}}
      responses:
        "200": {description: ok}
`

// swagger2SpecYAML is a minimal Swagger 2.x document as YAML.
const swagger2SpecYAML = `swagger: "2.0"
info: {title: Legacy, version: 1.0.0}
host: legacy.example.com
schemes: [https]
paths:
  /legacy:
    get:
      operationId: getLegacy
      responses:
        "200": {description: ok}
`

// execOpenAPIDryRun runs "openapi dry-run" under a root whose
// PersistentPreRunE fails, proving the command never loads a config file.
func execOpenAPIDryRun(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	withGlobalConfig(t, nil)

	root := &cobra.Command{
		Use:           "manifold",
		SilenceErrors: true,
		SilenceUsage:  true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return errors.New("config must not be loaded")
		},
	}
	root.AddCommand(newOpenAPICmd())

	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"openapi", "dry-run"}, args...))
	err = root.ExecuteContext(t.Context())
	return outBuf.String(), errBuf.String(), err
}

func TestOpenAPIDryRun_OpenAPI3_FromFileName(t *testing.T) {
	path := writeSpecFile(t, "petstore.openapi.yaml", petstoreSpecYAML)

	stdout, stderr, err := execOpenAPIDryRun(t, path)
	require.NoError(t, err)
	require.Empty(t, stderr)

	want := strings.Join([]string{
		"OK: " + path,
		"  format:   OpenAPI 3.0.3 (from file name)",
		"  base URL: https://petstore.example.com/v1",
		"  tools:    2",
		"",
		"TOOL        OPERATION         DESCRIPTION",
		"addpet      POST /pet         Add a new pet to the store",
		"getpetbyid  GET /pet/{petId}  Find pet by ID",
		"",
	}, "\n")
	require.Equal(t, want, stdout)
}

func TestOpenAPIDryRun_Swagger2_FromFileName(t *testing.T) {
	path := writeSpecFile(t, "legacy.swagger.yml", swagger2SpecYAML)

	stdout, stderr, err := execOpenAPIDryRun(t, path)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "OK: "+path+"\n")
	require.Contains(t, stdout, "  format:   Swagger 2.0 (from file name)\n")
	require.Contains(t, stdout, "  base URL: https://legacy.example.com\n")
	require.Contains(t, stdout, "getlegacy  GET /legacy")
}

func TestOpenAPIDryRun_FallsBackToVersionField(t *testing.T) {
	tests := []struct {
		name       string
		file       string
		content    string
		wantFormat string
	}{
		{"openapi yaml", "petstore.yaml", petstoreSpecYAML, "OpenAPI 3.0.3"},
		{"openapi json", "petstore.json", petstoreSpecJSON, "OpenAPI 3.0.0"},
		{"swagger json", "legacy.json", swagger2SpecJSON, "Swagger 2.0"},
		{"swagger yaml", "legacy.yaml", swagger2SpecYAML, "Swagger 2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSpecFile(t, tt.file, tt.content)

			stdout, _, err := execOpenAPIDryRun(t, path)
			require.NoError(t, err)
			require.Contains(t, stdout, "OK: "+path+"\n")
			require.Contains(t, stdout, "  format:   "+tt.wantFormat+" (from version field)\n")
		})
	}
}

func TestOpenAPIDryRun_NoBaseURL_WarnsButSucceeds(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)

	stdout, stderr, err := execOpenAPIDryRun(t, path)
	require.NoError(t, err)
	require.Contains(t, stdout, "OK: "+path+"\n")
	require.Contains(
		t,
		stdout,
		"  base URL: (not derivable from the spec; set mcpServers.<name>.baseURL)\n",
	)
	require.Contains(
		t,
		stdout,
		"uploadfile  POST /pet/{petId}/uploadImage  uploads an image [binary]",
	)
	require.Contains(
		t,
		stderr,
		"warning: baseURL is not set and could not be derived from the spec",
	)
}

func TestOpenAPIDryRun_NoOperations_WarnsButSucceeds(t *testing.T) {
	path := writeSpecFile(
		t,
		"empty.openapi.yaml",
		"openapi: 3.0.0\ninfo: {title: t, version: \"1\"}\nservers: [{url: https://x.example.com}]\npaths: {}\n",
	)

	stdout, stderr, err := execOpenAPIDryRun(t, path)
	require.NoError(t, err)
	require.Contains(t, stdout, "  tools:    0\n")
	require.NotContains(t, stdout, "TOOL")
	require.Contains(t, stderr, "warning: the spec has no operations")
}

func TestOpenAPIDryRun_Failures(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		content string
		wantErr string
	}{
		{
			"file name says openapi, document says swagger",
			"legacy.openapi.yaml", swagger2SpecYAML,
			`file name declares OpenAPI 3.x, but the document declares swagger: "2.0"`,
		},
		{
			"file name says swagger, document says openapi",
			"petstore.swagger.yaml", petstoreSpecYAML,
			`file name declares Swagger 2.x, but the document declares openapi: "3.0.3"`,
		},
		{
			"file name says openapi, document has no version",
			"plain.openapi.yaml", "info: {title: x}\n",
			`file name declares OpenAPI 3.x, but the document does not: document has neither`,
		},
		{
			"no file name hint and no version field",
			"plain.yaml", "foo: bar\n",
			`detect format: document has neither an "openapi" nor a "swagger" field`,
		},
		{
			"not YAML",
			"broken.yaml", "openapi: 3.0.0\npaths: [\n",
			"detect format: parse YAML",
		},
		{
			"unsupported version",
			"old.yaml", "openapi: \"2.0\"\n",
			`unsupported OpenAPI version "2.0"`,
		},
		{
			"unresolvable $ref",
			"badref.openapi.yaml",
			"openapi: 3.0.0\ninfo: {title: t, version: \"1\"}\npaths:\n  /a:\n    get:\n" +
				"      responses: {\"200\": {$ref: \"#/components/responses/missing\"}}\n",
			"load as OpenAPI 3.0.0 (from file name)",
		},
		{
			// The gateway's Swagger 2.x loader rejects an unquoted (numeric)
			// version, so dry-run must not report it as loadable.
			"swagger version written as a number",
			"legacy.swagger.yaml", strings.Replace(swagger2SpecYAML, `"2.0"`, "2.0", 1),
			"load as Swagger 2 (from file name)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeSpecFile(t, tt.file, tt.content)

			stdout, _, err := execOpenAPIDryRun(t, path)
			require.ErrorContains(t, err, tt.wantErr)
			require.ErrorContains(t, err, path+": ")
			require.Equal(t, "NG: "+path+"\n", stdout)
		})
	}
}

func TestOpenAPIDryRun_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.openapi.yaml")

	stdout, _, err := execOpenAPIDryRun(t, path)
	require.ErrorContains(t, err, "read file")
	require.Equal(t, "NG: "+path+"\n", stdout)
}

func TestOpenAPIDryRun_RequiresExactlyOneFile(t *testing.T) {
	_, _, err := execOpenAPIDryRun(t)
	require.ErrorContains(t, err, "accepts 1 arg(s), received 0")

	a := writeSpecFile(t, "a.openapi.yaml", petstoreSpecYAML)
	b := writeSpecFile(t, "b.openapi.yaml", petstoreSpecYAML)
	_, _, err = execOpenAPIDryRun(t, a, b)
	require.ErrorContains(t, err, "accepts 1 arg(s), received 2")
}
