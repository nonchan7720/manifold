package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// petstoreSpecJSONParamRequired is petstoreSpecJSON with a new required
// "verbose" query parameter on getPetById.
const petstoreSpecJSONParamRequired = `{
  "openapi": "3.0.0",
  "info": {"title": "Petstore", "version": "1.0.0"},
  "paths": {
    "/pet": {
      "post": {
        "operationId": "addPet",
        "summary": "Add a new pet to the store",
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/pet/{petId}": {
      "get": {
        "operationId": "getPetById",
        "summary": "Find pet by ID",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "verbose", "in": "query", "required": true, "schema": {"type": "boolean"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/pet/{petId}/uploadImage": {
      "post": {
        "operationId": "uploadFile",
        "summary": "uploads an image",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/octet-stream": {"schema": {"type": "string", "format": "binary"}}
            }
          }
        }
      }
    }
  }
}`

func TestOpenAPIDiff_NoChanges(t *testing.T) {
	setupCheckServer(t)

	stdout, stderr, err := execOpenAPITools(t, "diff")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "petstore: no API changes\n", stdout)
}

func TestOpenAPIDiff_RequiredParamAdded_FailsOnErr(t *testing.T) {
	specPath, outPath := setupCheckServer(t)
	before, err := os.ReadFile(outPath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff")
	require.Error(t, err)
	require.Contains(t, err.Error(), "changes at or above error level found in 1 server(s)")
	require.Contains(t, stdout, "petstore: 1 breaking change (1 error, 0 warnings)")
	require.Contains(
		t, stdout,
		"  error    GET /pet/{petId} (getpetbyid)  new-required-request-parameter: "+
			"added the new required `query` request parameter `verbose`",
	)

	after, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "diff must never write")
}

func TestOpenAPIDiff_OperationRemoved_NamesRemovedTool(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolRemoved), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff")
	require.Error(t, err)
	require.Contains(t, stdout, "POST /pet/{petId}/uploadImage (uploadfile)")
	require.Contains(t, stdout, "api-path-removed-without-deprecation")
}

func TestOpenAPIDiff_FailOnNone_DoesNotFail(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	for _, failOn := range []string{"", "NONE"} {
		stdout, _, err := execOpenAPITools(t, "diff", "--fail-on", failOn)
		require.NoError(t, err, failOn)
		require.Contains(t, stdout, "1 breaking change", failOn)
	}
}

func TestOpenAPIDiff_NonBreakingAddition(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolAdded), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff")
	require.NoError(t, err, "an info-level change must not fail with the default --fail-on")
	require.Contains(t, stdout, "petstore: no breaking changes, 1 non-breaking")
	require.Contains(t, stdout, "  info     DELETE /pet/{petId} (deletepet)  endpoint-added: ")

	_, _, err = execOpenAPITools(t, "diff", "--fail-on", "INFO")
	require.Error(t, err)
	require.Contains(t, err.Error(), "changes at or above info level found in 1 server(s)")
}

func TestOpenAPIDiff_JSON(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--format", "json")
	require.Error(t, err)

	var got map[string]struct {
		Breaking int `json:"breaking"`
		Errors   int `json:"errors"`
		Warnings int `json:"warnings"`
		Changes  []struct {
			Level     string `json:"level"`
			ID        string `json:"id"`
			Operation string `json:"operation"`
			Message   string `json:"message"`
			Tool      string `json:"tool"`
		} `json:"changes"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Contains(t, got, "petstore")
	p := got["petstore"]
	require.Equal(t, 1, p.Breaking)
	require.Equal(t, 1, p.Errors)
	require.NotEmpty(t, p.Changes)
	require.Equal(t, "error", p.Changes[0].Level)
	require.Equal(t, "new-required-request-parameter", p.Changes[0].ID)
	require.Equal(t, "GET /pet/{petId}", p.Changes[0].Operation)
	require.Equal(t, "getpetbyid", p.Changes[0].Tool)
}

func TestOpenAPIDiff_JSON_NoChanges_EmptyArray(t *testing.T) {
	setupCheckServer(t)

	stdout, _, err := execOpenAPITools(t, "diff", "--format", "json")
	require.NoError(t, err)
	require.Contains(t, stdout, `"changes": []`)
}

func TestOpenAPIDiff_Markdown(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--format", "markdown")
	require.Error(t, err)
	require.True(t, strings.HasPrefix(stdout, "### petstore\n\n1 breaking change"), stdout)
	require.Contains(t, stdout, "| Level | Operation | Tool | Rule | Message |")
	require.Contains(
		t, stdout,
		"| error | `GET /pet/{petId}` | `getpetbyid` | `new-required-request-parameter` | ",
	)
}

func TestOpenAPIDiff_InvalidFlags(t *testing.T) {
	setupCheckServer(t)

	_, _, err := execOpenAPITools(t, "diff", "--fail-on", "FATAL")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--fail-on")

	_, _, err = execOpenAPITools(t, "diff", "--format", "xml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--format")
}

func TestOpenAPIDiff_SkipsServerWithoutToolsFile(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: specPath, BaseURL: "http://example.local"},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "diff")
	require.NoError(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, `server "petstore": no tools.file configured, skipping`)
}

func TestOpenAPIDiff_MissingToolsFile_Errors(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath := filepath.Join(t.TempDir(), "missing.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})

	_, stderr, err := execOpenAPITools(t, "diff")
	require.Error(t, err)
	require.Contains(t, stderr, `is missing (run "manifold openapi generate")`)
}

func TestOpenAPIGenerateCheck_ShowsBreakingChanges(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, err.Error(), "drift detected in 1 server(s)")
	require.Contains(t, stdout, "  embedded spec differs from what the live spec produces")
	require.Contains(t, stdout, "  compatibility: 1 breaking change (1 error, 0 warnings)")
	require.Contains(
		t, stdout,
		"    error    GET /pet/{petId} (getpetbyid)  new-required-request-parameter: ",
	)
	require.Contains(t, stdout, "~ changed: getpetbyid (inputSchema)")
}

func TestOpenAPIGenerateCheck_NonBreakingAddition_SummaryOnly(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolAdded), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err, "--check still fails on any drift")
	require.Contains(t, stdout, "  compatibility: no breaking changes, 1 non-breaking")
	require.NotContains(t, stdout, "endpoint-added", "non-breaking changes are only counted")
}

func TestOpenAPIGenerateCheck_EmbeddedSpecEdited_NoAPIChanges(t *testing.T) {
	_, outPath := setupCheckServer(t)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)
	edited := strings.Replace(string(raw), "title: Petstore", "title: Petstore (edited)", 1)
	require.NotEqual(t, string(raw), edited)
	require.NoError(t, os.WriteFile(outPath, []byte(edited), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, stdout, "  embedded spec differs from what the live spec produces")
	require.Contains(t, stdout, "  compatibility: no API changes")
}

// petstoreSpecJSONParamDescribed is petstoreSpecJSON with a description on
// getPetById's petId parameter: oasdiff reports nothing, but the generated
// inputSchema changes.
var petstoreSpecJSONParamDescribed = strings.Replace(
	petstoreSpecJSON,
	`{"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}`,
	`{"name": "petId", "in": "path", "required": true, "description": "ID of pet", `+
		`"schema": {"type": "integer"}}`,
	1,
)

func TestOpenAPIDiffByTool_NoChanges(t *testing.T) {
	setupCheckServer(t)

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool")
	require.NoError(t, err)
	require.Equal(t, "petstore: 0 of 3 tools affected, no API changes\n", stdout)
}

func TestOpenAPIDiffByTool_RequiredParamAdded_Changed(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool")
	require.Error(t, err)
	require.Contains(t, err.Error(), "changes at or above error level found in 1 server(s)")
	require.Equal(
		t,
		"petstore: 1 of 3 tools affected, 1 breaking change (1 error, 0 warnings), 1 non-breaking\n"+
			"  error    changed  getpetbyid (GET /pet/{petId})\n"+
			"    error    new-required-request-parameter: "+
			"added the new required `query` request parameter `verbose`\n"+
			"  (spec-wide)\n"+
			"    info     -  api-version-not-bumped: "+
			"a breaking change was detected but the version is still `1.0.0`\n",
		stdout,
	)
}

func TestOpenAPIDiffByTool_OperationRemoved_Removed(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolRemoved), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool")
	require.Error(t, err)
	require.Contains(t, stdout, "petstore: 1 of 3 tools affected, ")
	require.Contains(t, stdout, "  error    removed  uploadfile (POST /pet/{petId}/uploadImage)\n")
	require.Contains(t, stdout, "    error    api-path-removed-without-deprecation: ")
	require.NotContains(t, stdout, "getpetbyid", "unaffected tools are omitted")
	require.NotContains(t, stdout, "addpet", "unaffected tools are omitted")
}

func TestOpenAPIDiffByTool_OperationAdded_Added(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolAdded), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool")
	require.NoError(t, err, "an added tool must not fail with the default --fail-on")
	require.Equal(
		t,
		"petstore: 1 of 4 tools affected, no breaking changes, 1 non-breaking\n"+
			"  info     added    deletepet (DELETE /pet/{petId})\n"+
			"    info     endpoint-added: endpoint added\n",
		stdout,
	)
}

func TestOpenAPIDiffByTool_SchemaOnlyChange_ChangedWithNote(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamDescribed), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool", "--fail-on", "INFO")
	require.NoError(t, err, "a change oasdiff doesn't report has no level to fail on")
	require.Equal(
		t,
		"petstore: 1 of 3 tools affected, no API changes\n"+
			"  none     changed  getpetbyid (GET /pet/{petId})\n"+
			"    note: inputSchema changed in the generated tools, but oasdiff reported no change\n",
		stdout,
	)
}

func TestOpenAPIDiffByTool_JSON(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool", "--format", "json")
	require.Error(t, err)

	type change struct {
		Level     string `json:"level"`
		ID        string `json:"id"`
		Operation string `json:"operation"`
		Message   string `json:"message"`
	}
	var got map[string]struct {
		Affected int `json:"affected"`
		Total    int `json:"total"`
		Tools    []struct {
			Name      string   `json:"name"`
			Operation string   `json:"operation"`
			Status    string   `json:"status"`
			Level     string   `json:"level"`
			Changes   []change `json:"changes"`
		} `json:"tools"`
		SpecWide []change `json:"specWide"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Contains(t, got, "petstore")
	p := got["petstore"]
	require.Equal(t, 1, p.Affected)
	require.Equal(t, 3, p.Total)
	require.Len(t, p.Tools, 1)
	tool := p.Tools[0]
	require.Equal(t, "getpetbyid", tool.Name)
	require.Equal(t, "GET /pet/{petId}", tool.Operation)
	require.Equal(t, "changed", tool.Status)
	require.Equal(t, "error", tool.Level)
	require.Equal(t, []change{{
		Level: "error", ID: "new-required-request-parameter",
		Message: "added the new required `query` request parameter `verbose`",
	}}, tool.Changes)
	require.Len(t, p.SpecWide, 1)
	require.Equal(t, "api-version-not-bumped", p.SpecWide[0].ID)
	require.Empty(t, p.SpecWide[0].Operation)
}

func TestOpenAPIDiffByTool_JSON_NoChanges_EmptyArrays(t *testing.T) {
	setupCheckServer(t)

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool", "--format", "json")
	require.NoError(t, err)
	require.Contains(t, stdout, `"affected": 0,`)
	require.Contains(t, stdout, `"total": 3,`)
	require.Contains(t, stdout, `"tools": []`)
	require.Contains(t, stdout, `"specWide": []`)
}

func TestOpenAPIDiffByTool_Markdown(t *testing.T) {
	specPath, _ := setupCheckServer(t)
	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONParamRequired), 0o600))

	stdout, _, err := execOpenAPITools(t, "diff", "--by-tool", "--format", "markdown")
	require.Error(t, err)
	require.True(
		t, strings.HasPrefix(stdout, "### petstore\n\n1 of 3 tools affected, 1 breaking change"),
		stdout,
	)
	require.Contains(t, stdout, "| Tool | Operation | Status | Level | Rule | Message |")
	require.Contains(
		t, stdout,
		"| `getpetbyid` | `GET /pet/{petId}` | **changed** (error) | error | "+
			"`new-required-request-parameter` | ",
	)
	require.Contains(
		t, stdout, "| _(spec-wide)_ | - | | info | `api-version-not-bumped` | ",
	)
}
