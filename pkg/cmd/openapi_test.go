package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/stretchr/testify/require"
)

// petstoreSpecJSON is a minimal OpenAPI 3 spec with three operations: two
// plain ones and one binary-response one, used to exercise "openapi tools"
// end to end without a network fetch.
const petstoreSpecJSON = `{
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
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
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

// swagger2SpecJSON is the minimal Swagger 2.x document needed for
// oastomcptool.LoadSpecSource to detect the "swagger" format and skip it.
const swagger2SpecJSON = `{
  "swagger": "2.0",
  "info": {"title": "Legacy", "version": "1.0.0"},
  "paths": {
    "/legacy": {
      "get": {
        "operationId": "getLegacy",
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

// writeSpecFile writes content under t.TempDir() as name and returns its path.
func writeSpecFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// execOpenAPITools dispatches args (e.g. "tools", "--json") into a freshly
// built newOpenAPICmd() tree and returns its stdout/stderr/error.
//
// It intentionally does NOT call cobra's Execute()/ExecuteC(): those invoke
// the process-global cobra.OnInitialize(initialize) hook (registered once in
// root.go's init()) on every call regardless of which command tree is being
// executed, which reloads config.Load (cached for the whole test binary via
// a package-level sync.Once) and reassigns globalConfig out from under the
// value the test just installed via withGlobalConfig. Finding the target
// subcommand, parsing its flags, and calling RunE directly exercises the
// same flag definitions and RunE wiring while avoiding that global hook.
func execOpenAPITools(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newOpenAPICmd()
	target, flagArgs, err := root.Find(args)
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	target.SetOut(&outBuf)
	target.SetErr(&errBuf)
	target.SetContext(t.Context())
	require.NoError(t, target.ParseFlags(flagArgs))

	err = target.RunE(target, target.Flags().Args())
	return outBuf.String(), errBuf.String(), err
}

func TestOpenAPITools_Table(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "tools")
	require.NoError(t, err)
	require.Empty(t, stderr)

	want := strings.Join([]string{
		"SERVER    TOOL        OPERATION                      DESCRIPTION",
		"petstore  addpet      POST /pet                      Add a new pet to the store",
		"petstore  getpetbyid  GET /pet/{petId}               Find pet by ID",
		"petstore  uploadfile  POST /pet/{petId}/uploadImage  uploads an image [binary]",
		"",
	}, "\n")
	require.Equal(t, want, stdout)
}

func TestOpenAPITools_Table_MultipleServersSortedByName(t *testing.T) {
	zPath := writeSpecFile(t, "z.json", petstoreSpecJSON)
	aPath := writeSpecFile(t, "a.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"zebra": &config.Server{Spec: zPath, BaseURL: "http://example.local"},
			"alpha": &config.Server{Spec: aPath, BaseURL: "http://example.local"},
		},
	})

	stdout, _, err := execOpenAPITools(t, "tools")
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.True(t, strings.HasPrefix(lines[1], "alpha"), "expected alpha before zebra: %v", lines)
	require.True(
		t, strings.HasPrefix(lines[len(lines)-1], "zebra"), "expected zebra last: %v", lines,
	)
}

type jsonToolEntry struct {
	Name           string         `json:"name"`
	Operation      string         `json:"operation"`
	Description    string         `json:"description"`
	BinaryResponse bool           `json:"binaryResponse"`
	InputSchema    map[string]any `json:"inputSchema"`
}

func TestOpenAPITools_JSON(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "tools", "--json")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.True(t, strings.HasSuffix(stdout, "\n"))

	var got map[string][]jsonToolEntry
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)

	tools := got["petstore"]
	require.Len(t, tools, 3)
	require.Equal(t, []string{"addpet", "getpetbyid", "uploadfile"}, []string{
		tools[0].Name, tools[1].Name, tools[2].Name,
	})

	upload := tools[2]
	require.Equal(t, "POST /pet/{petId}/uploadImage", upload.Operation)
	require.Equal(t, "uploads an image", upload.Description)
	require.True(t, upload.BinaryResponse)
	require.NotNil(t, upload.InputSchema)
	require.Equal(t, "object", upload.InputSchema["type"])

	getByID := tools[1]
	require.False(t, getByID.BinaryResponse)
	require.Equal(t, "GET /pet/{petId}", getByID.Operation)
}

func TestOpenAPITools_ToolFlag_Found(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	stdout, _, err := execOpenAPITools(t, "tools", "--tool", "getpetbyid")
	require.NoError(t, err)
	require.Contains(t, stdout, "server: petstore")
	require.Contains(t, stdout, "name: getpetbyid")
	require.Contains(t, stdout, "operation: GET /pet/{petId}")
	require.Contains(t, stdout, "description: Find pet by ID")
	require.Contains(t, stdout, "binaryResponse: false")
	require.Contains(t, stdout, `"type": "object"`)
	// only the requested tool should appear
	require.NotContains(t, stdout, "name: addpet")
}

func TestOpenAPITools_ToolFlag_JSON(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	stdout, _, err := execOpenAPITools(t, "tools", "--tool", "getpetbyid", "--json")
	require.NoError(t, err)

	var got map[string][]jsonToolEntry
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	require.Len(t, got, 1)
	require.Len(t, got["petstore"], 1)
	require.Equal(t, "getpetbyid", got["petstore"][0].Name)
}

func TestOpenAPITools_ToolFlag_Unknown(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	_, _, err := execOpenAPITools(t, "tools", "--tool", "doesnotexist")
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown tool "doesnotexist"`)
}

func TestOpenAPITools_ServerFlag_Unknown(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	_, _, err := execOpenAPITools(t, "tools", "--server", "doesnotexist")
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown server "doesnotexist"`)
}

func TestOpenAPITools_ServerFlag_Restricts(t *testing.T) {
	path := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: path, BaseURL: "http://example.local"},
			"other":    &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	stdout, _, err := execOpenAPITools(t, "tools", "--server", "petstore")
	require.NoError(t, err)
	require.Contains(t, stdout, "petstore")
	require.NotContains(t, stdout, "other")
}

func TestOpenAPITools_Swagger2Skipped(t *testing.T) {
	path := writeSpecFile(t, "legacy.json", swagger2SpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"legacy": &config.Server{Spec: path, BaseURL: "http://example.local"},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "tools")
	require.NoError(t, err, "a skipped swagger2 server must not fail the command")
	require.Contains(
		t, stderr,
		`server "legacy": Swagger 2.x is not supported by "openapi tools", skipping`,
	)
	// no tools were printed for it
	require.NotContains(t, stdout, "legacy")
}

func TestOpenAPITools_LoadFailure_OtherServersStillPrint(t *testing.T) {
	goodPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: goodPath, BaseURL: "http://example.local"},
			"broken": &config.Server{
				Spec: "/nonexistent/does-not-exist.json", BaseURL: "http://example.local",
			},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "tools")
	require.Error(t, err)
	require.Contains(t, stderr, `server "broken"`)

	// the good server's tools were still printed
	require.Contains(t, stdout, "petstore")
	require.Contains(t, stdout, "addpet")
	require.NotContains(t, stdout, "broken")
}
