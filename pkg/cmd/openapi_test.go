package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
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

// petstoreSpecJSONInfoDescriptionChanged is petstoreSpecJSON with only
// info.description added — the spec bytes (and thus source.sha256) change,
// but every operation is untouched, so the derived tool list is identical.
const petstoreSpecJSONInfoDescriptionChanged = `{
  "openapi": "3.0.0",
  "info": {"title": "Petstore", "version": "1.0.0", "description": "now with a description"},
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

// petstoreSpecJSONToolAdded is petstoreSpecJSON with an extra DELETE
// /pet/{petId} operation ("deletepet"), everything else unchanged.
const petstoreSpecJSONToolAdded = `{
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
      },
      "delete": {
        "operationId": "deletePet",
        "summary": "Deletes a pet",
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

// petstoreSpecJSONToolRemoved is petstoreSpecJSON with the
// /pet/{petId}/uploadImage operation ("uploadfile") dropped entirely.
const petstoreSpecJSONToolRemoved = `{
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
    }
  }
}`

// petstoreSpecJSONDescriptionChanged is petstoreSpecJSON with only
// getPetById's summary changed, so only its derived description differs.
const petstoreSpecJSONDescriptionChanged = `{
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
        "summary": "Find a pet by its ID",
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

// petstoreSpecJSONSchemaChanged is petstoreSpecJSON with getPetById's petId
// parameter changed from an integer to a string, so only its derived
// inputSchema differs.
const petstoreSpecJSONSchemaChanged = `{
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
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}
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

// --- generate ---

func TestOpenAPIGenerate_AllServers_WritesToolsFile(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath := filepath.Join(t.TempDir(), "generated", "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, `server "petstore": wrote `+outPath+` (3 tools)`)

	f, err := os.Open(outPath)
	require.NoError(t, err)
	defer f.Close()
	g, err := oastomcptool.ReadGeneratedCatalog(f)
	require.NoError(t, err)
	require.Equal(t, 1, g.Version)
	require.Equal(t, oastomcptool.SpecFormatOpenAPI3, g.Format)
	require.Equal(t, specPath, g.Source.Spec)

	names := make([]string, len(g.Tools))
	for i, tl := range g.Tools {
		names[i] = tl.Name
	}
	sorted := slices.Clone(names)
	sort.Strings(sorted)
	require.Equal(t, sorted, names)
	require.Equal(t, []string{"addpet", "getpetbyid", "uploadfile"}, names)

	_, _, err = oastomcptool.LoadGeneratedSpecSource(t.Context(), outPath)
	require.NoError(t, err)
}

func TestOpenAPIGenerate_ServerFlagWithOutput_CreatesParentDirs(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath := filepath.Join(t.TempDir(), "a", "b", "c", "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: specPath, BaseURL: "http://example.local"},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "generate", "--server", "petstore", "-o", outPath)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "wrote "+outPath)

	_, err = os.Stat(outPath)
	require.NoError(t, err)
}

func TestOpenAPIGenerate_OutputWithoutServer_Error(t *testing.T) {
	withGlobalConfig(t, &config.Config{MCPServer: config.Servers{}})

	_, _, err := execOpenAPITools(t, "generate", "-o", "./out.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--output requires --server")
}

func TestOpenAPIGenerate_ServerWithoutToolsFileAndNoOutput_Error(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: specPath, BaseURL: "http://example.local"},
		},
	})

	_, stderr, err := execOpenAPITools(t, "generate", "--server", "petstore")
	require.Error(t, err)
	require.Contains(t, stderr, `server "petstore"`)
	require.Contains(t, stderr, "no tools.file configured")
}

func TestOpenAPIGenerate_AllServers_SkipsServerWithoutToolsFile(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath := filepath.Join(t.TempDir(), "generated", "withfile.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"withfile": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
			"nofile": &config.Server{Spec: specPath, BaseURL: "http://example.local"},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)
	require.Contains(t, stderr, `server "nofile": no tools.file configured, skipping`)
	require.Contains(t, stdout, `server "withfile": wrote `+outPath)

	_, err = os.Stat(outPath)
	require.NoError(t, err, "withfile's generated file should have been written")
}

func TestOpenAPIGenerate_Swagger2Skipped(t *testing.T) {
	specPath := writeSpecFile(t, "legacy.json", swagger2SpecJSON)
	outPath := filepath.Join(t.TempDir(), "generated", "legacy.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"legacy": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "generate")
	require.NoError(t, err, "a skipped swagger2 server must not fail the command")
	require.Contains(t, stderr, `server "legacy"`)
	require.Contains(t, stderr, "does not support Swagger 2.x")
	require.NotContains(t, stdout, "legacy")

	_, err = os.Stat(outPath)
	require.True(t, os.IsNotExist(err), "no file should have been written for a swagger2 server")
}

func TestOpenAPIGenerate_DeterministicExceptFetchedAt(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	dir := t.TempDir()
	path1 := filepath.Join(dir, "run1.yaml")
	path2 := filepath.Join(dir, "run2.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: specPath, BaseURL: "http://example.local"},
		},
	})

	_, _, err := execOpenAPITools(t, "generate", "--server", "petstore", "-o", path1)
	require.NoError(t, err)
	_, _, err = execOpenAPITools(t, "generate", "--server", "petstore", "-o", path2)
	require.NoError(t, err)

	b1, err := os.ReadFile(path1)
	require.NoError(t, err)
	b2, err := os.ReadFile(path2)
	require.NoError(t, err)

	blankFetchedAt := func(b []byte) string {
		lines := strings.Split(string(b), "\n")
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "fetchedAt:") {
				lines[i] = "fetchedAt: BLANKED"
			}
		}
		return strings.Join(lines, "\n")
	}
	require.Equal(t, blankFetchedAt(b1), blankFetchedAt(b2))
	// sanity check the two runs actually did have distinct fetchedAt values
	// captured (otherwise the blanking above wouldn't be exercising anything)
	require.NotEqual(t, string(b1), "")
}

// --- tools reading a generated file ---

func TestOpenAPITools_GeneratedFile_MatchesFromSpec(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	genPath := filepath.Join(t.TempDir(), "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: genPath},
			},
		},
	})

	_, _, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)

	fromFile, stderr1, err := execOpenAPITools(t, "tools")
	require.NoError(t, err)
	require.Empty(t, stderr1)

	fromSpec, stderr2, err := execOpenAPITools(t, "tools", "--from-spec")
	require.NoError(t, err)
	require.Empty(t, stderr2)

	require.Equal(t, fromSpec, fromFile)
}

func TestOpenAPITools_GeneratedFileMissing_FailsWithGenerateHint(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	genPath := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: genPath},
			},
		},
	})

	_, stderr, err := execOpenAPITools(t, "tools")
	require.Error(t, err)
	require.Contains(t, stderr, `server "petstore"`)
	require.Contains(t, stderr, `manifold openapi generate`)

	fromSpec, stderr2, err := execOpenAPITools(t, "tools", "--from-spec")
	require.NoError(t, err)
	require.Empty(t, stderr2)
	require.Contains(t, fromSpec, "addpet")
}

func TestOpenAPITools_StaleGeneratedFile_FailsWithStale(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	genPath := filepath.Join(t.TempDir(), "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: genPath},
			},
		},
	})

	_, _, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)

	raw, err := os.ReadFile(genPath)
	require.NoError(t, err)
	edited := strings.Replace(
		string(raw), "description: Find pet by ID", "description: mutated description", 1,
	)
	require.NotEqual(t, string(raw), edited, "expected to find the tools-section description")
	require.NoError(t, os.WriteFile(genPath, []byte(edited), 0o600))

	_, stderr, err := execOpenAPITools(t, "tools")
	require.Error(t, err)
	require.Contains(t, stderr, "stale")
}

// --- tools.file-only servers (no spec configured) ---

// setupToolsFileOnlyServer generates a tools.file for "petstore" from a live
// spec (so the file matches what that spec produces), then reinstalls the
// config with spec dropped entirely — the case config.Server.IsOpenAPI now
// allows for starting the gateway (spec is only needed to (re)generate the
// file, enforced by the CLI, not the gateway).
func setupToolsFileOnlyServer(t *testing.T) (genPath string) {
	t.Helper()
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	genPath = filepath.Join(t.TempDir(), "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: genPath},
			},
		},
	})
	_, _, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)

	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				BaseURL: "http://example.local",
				Tools:   &config.ToolsConfig{File: genPath},
			},
		},
	})
	return genPath
}

func TestOpenAPITools_ToolsFileOnlyServer_NoSpec_ReadsGeneratedFile(t *testing.T) {
	setupToolsFileOnlyServer(t)

	stdout, stderr, err := execOpenAPITools(t, "tools")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, "addpet")
	require.Contains(t, stdout, "getpetbyid")
	require.Contains(t, stdout, "uploadfile")
}

func TestOpenAPITools_ToolsFileOnlyServer_FromSpec_Errors(t *testing.T) {
	setupToolsFileOnlyServer(t)

	_, stderr, err := execOpenAPITools(t, "tools", "--from-spec")
	require.Error(t, err)
	require.Contains(t, stderr, `server "petstore": --from-spec requires spec to be configured`)
}

func TestOpenAPIGenerate_ToolsFileOnlyServer_NoSpec_Errors(t *testing.T) {
	genPath := setupToolsFileOnlyServer(t)

	// A second, spec-having server must still run despite petstore failing.
	otherSpecPath := writeSpecFile(t, "other.json", petstoreSpecJSON)
	otherOutPath := filepath.Join(t.TempDir(), "other.yaml")
	globalConfig.MCPServer["other"] = &config.Server{
		Spec: otherSpecPath, BaseURL: "http://example.local",
		Tools: &config.ToolsConfig{File: otherOutPath},
	}

	stdout, stderr, err := execOpenAPITools(t, "generate")
	require.Error(t, err)
	require.Contains(t, stderr, `server "petstore": spec is required to generate the tools file`)
	require.Contains(t, stdout, `server "other": wrote `+otherOutPath)

	_, statErr := os.Stat(genPath)
	require.NoError(t, statErr, "petstore's already-generated file must be left untouched")
}

func TestOpenAPIGenerateCheck_ToolsFileOnlyServer_NoSpec_Errors(t *testing.T) {
	setupToolsFileOnlyServer(t)

	_, stderr, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, stderr, `server "petstore": spec is required to generate the tools file`)
}

func TestSelectOpenAPIServers_IncludesToolsFileOnlyServers(t *testing.T) {
	cfg := &config.Config{
		MCPServer: config.Servers{
			"toolsfile-only": &config.Server{
				BaseURL: "http://example.local",
				Tools:   &config.ToolsConfig{File: "./generated/petstore.yaml"},
			},
			"spec-only": &config.Server{
				Spec: "openapi.json", BaseURL: "http://example.local",
			},
			"mcp-backend": &config.Server{
				Transport: config.MCPTransportHTTP, URL: "http://example.local",
			},
		},
	}
	names, err := selectOpenAPIServers(cfg, "")
	require.NoError(t, err)
	require.Equal(t, []string{"spec-only", "toolsfile-only"}, names)
}

// --- generate --check ---

// setupCheckServer generates outPath for "petstore" (source specPath) and
// returns the config, ready for a "generate --check" test to mutate
// specPath and re-check.
func setupCheckServer(t *testing.T) (specPath, outPath string) {
	t.Helper()
	specPath = writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath = filepath.Join(t.TempDir(), "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})
	_, _, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)
	return specPath, outPath
}

func TestOpenAPIGenerateCheck_UpToDate_FileUntouched(t *testing.T) {
	_, outPath := setupCheckServer(t)

	before, err := os.ReadFile(outPath)
	require.NoError(t, err)

	stdout, stderr, err := execOpenAPITools(t, "generate", "--check")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, fmt.Sprintf("server \"petstore\": up to date (%s)\n", outPath), stdout)

	after, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "--check must never write")
}

func TestOpenAPIGenerateCheck_MissingFile(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Empty(t, stderr)
	require.Contains(
		t, stdout,
		fmt.Sprintf(`server "petstore": %s is missing (run "manifold openapi generate")`, outPath),
	)
	require.Contains(t, err.Error(), "drift detected in 1 server(s)")

	_, statErr := os.Stat(outPath)
	require.True(t, os.IsNotExist(statErr), "--check must never create the file")
}

func TestOpenAPIGenerateCheck_SpecChanged_ToolsIdentical(t *testing.T) {
	specPath, outPath := setupCheckServer(t)
	before, err := os.ReadFile(outPath)
	require.NoError(t, err)

	require.NoError(
		t, os.WriteFile(specPath, []byte(petstoreSpecJSONInfoDescriptionChanged), 0o600),
	)

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, err.Error(), "drift detected in 1 server(s)")
	require.Contains(t, stdout, `server "petstore": drift detected`)
	require.Regexp(t, `spec changed \(sha256 [0-9a-f]{8}… → [0-9a-f]{8}…\)`, stdout)
	require.NotContains(t, stdout, "+ added")
	require.NotContains(t, stdout, "- removed")
	require.NotContains(t, stdout, "~ changed")
	require.Contains(t, stdout, `run "manifold openapi generate" to update`)

	after, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "--check must never write")
}

// TestOpenAPIGenerateCheck_EmbeddedSpecEdited_DriftDetected covers Finding 1:
// hand-editing only the generated file's "spec" section (the internalized
// document LoadGeneratedSpecSource/BuildCatalog actually run from at gateway
// startup) — with the upstream spec bytes and the "tools" section both left
// alone — must still be reported as drift, not "up to date".
func TestOpenAPIGenerateCheck_EmbeddedSpecEdited_DriftDetected(t *testing.T) {
	_, outPath := setupCheckServer(t)

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err)
	edited := strings.Replace(string(raw), "description: ok", "description: ok, edited by hand", 1)
	require.NotEqual(
		t, string(raw), edited, "expected to find a response description in the embedded spec",
	)
	require.NoError(t, os.WriteFile(outPath, []byte(edited), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, err.Error(), "drift detected in 1 server(s)")
	require.Contains(t, stdout, `server "petstore": drift detected`)
	require.Contains(t, stdout, "  embedded spec differs from what the live spec produces")
	require.NotContains(t, stdout, "spec changed (sha256")
	require.NotContains(t, stdout, "+ added")
	require.NotContains(t, stdout, "- removed")
	require.NotContains(t, stdout, "~ changed")
	require.Contains(t, stdout, `run "manifold openapi generate" to update`)

	after, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, edited, string(after), "--check must never write")
}

// TestOpenAPIGenerateCheck_UpToDate_RealFixture guards against a false
// positive from the new embedded-spec comparison (Finding 1): a fresh
// generate immediately followed by --check must report "up to date" even
// for a large, real-world spec that exercises InternalizeRefs and a full
// YAML round trip (petstore_oas.json, shared with pkg/internal/mcpsrv's own
// tests), not just the small inline fixtures above.
func TestOpenAPIGenerateCheck_UpToDate_RealFixture(t *testing.T) {
	fixture, err := os.ReadFile(
		filepath.Join("..", "internal", "mcpsrv", "fixtures", "petstore_oas.json"),
	)
	require.NoError(t, err)
	specPath := writeSpecFile(t, "petstore_oas.json", string(fixture))
	outPath := filepath.Join(t.TempDir(), "petstore.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{
				Spec: specPath, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})

	_, _, err = execOpenAPITools(t, "generate")
	require.NoError(t, err)

	stdout, stderr, err := execOpenAPITools(t, "generate", "--check")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, fmt.Sprintf("server \"petstore\": up to date (%s)\n", outPath), stdout)
}

func TestOpenAPIGenerateCheck_ToolAdded(t *testing.T) {
	specPath, outPath := setupCheckServer(t)
	before, err := os.ReadFile(outPath)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolAdded), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, stdout, `server "petstore": drift detected`)
	require.Contains(t, stdout, "+ added: deletepet (DELETE /pet/{petId})")
	require.NotContains(t, stdout, "- removed")
	require.NotContains(t, stdout, "~ changed")

	after, err := os.ReadFile(outPath)
	require.NoError(t, err)
	require.Equal(t, before, after, "--check must never write")
}

func TestOpenAPIGenerateCheck_ToolRemoved(t *testing.T) {
	specPath, _ := setupCheckServer(t)

	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONToolRemoved), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, stdout, `server "petstore": drift detected`)
	require.Contains(t, stdout, "- removed: uploadfile (POST /pet/{petId}/uploadImage)")
	require.NotContains(t, stdout, "+ added")
	require.NotContains(t, stdout, "~ changed")
}

func TestOpenAPIGenerateCheck_DescriptionChanged(t *testing.T) {
	specPath, _ := setupCheckServer(t)

	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONDescriptionChanged), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, stdout, `server "petstore": drift detected`)
	require.Contains(t, stdout, "~ changed: getpetbyid (description)")
	require.NotContains(t, stdout, "+ added")
	require.NotContains(t, stdout, "- removed")
}

func TestOpenAPIGenerateCheck_InputSchemaChanged(t *testing.T) {
	specPath, _ := setupCheckServer(t)

	require.NoError(t, os.WriteFile(specPath, []byte(petstoreSpecJSONSchemaChanged), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, stdout, `server "petstore": drift detected`)
	require.Contains(t, stdout, "~ changed: getpetbyid (inputSchema)")
	require.NotContains(t, stdout, "+ added")
	require.NotContains(t, stdout, "- removed")
}

func TestOpenAPIGenerateCheck_OutputWithServer_ComparesAgainstOutputPath(t *testing.T) {
	specPath := writeSpecFile(t, "petstore.json", petstoreSpecJSON)
	outPath := filepath.Join(t.TempDir(), "custom.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"petstore": &config.Server{Spec: specPath, BaseURL: "http://example.local"},
		},
	})

	_, _, err := execOpenAPITools(t, "generate", "--server", "petstore", "-o", outPath)
	require.NoError(t, err)

	stdout, stderr, err := execOpenAPITools(
		t, "generate", "--check", "--server", "petstore", "-o", outPath,
	)
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Contains(t, stdout, fmt.Sprintf("up to date (%s)", outPath))
}

func TestOpenAPIGenerateCheck_OutputWithoutServer_Error(t *testing.T) {
	withGlobalConfig(t, &config.Config{MCPServer: config.Servers{}})

	_, _, err := execOpenAPITools(t, "generate", "--check", "-o", "./out.yaml")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--output requires --server")
}

func TestOpenAPIGenerateCheck_MultipleServers_OneUpToDateOneDrifts(t *testing.T) {
	specPathAlpha := writeSpecFile(t, "alpha.json", petstoreSpecJSON)
	specPathZebra := writeSpecFile(t, "zebra.json", petstoreSpecJSON)
	outPathAlpha := filepath.Join(t.TempDir(), "alpha.yaml")
	outPathZebra := filepath.Join(t.TempDir(), "zebra.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"alpha": &config.Server{
				Spec: specPathAlpha, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPathAlpha},
			},
			"zebra": &config.Server{
				Spec: specPathZebra, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPathZebra},
			},
		},
	})

	_, _, err := execOpenAPITools(t, "generate")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(specPathZebra, []byte(petstoreSpecJSONToolAdded), 0o600))

	stdout, _, err := execOpenAPITools(t, "generate", "--check")
	require.Error(t, err)
	require.Contains(t, err.Error(), "drift detected in 1 server(s)")
	require.Contains(t, stdout, fmt.Sprintf(`server "alpha": up to date (%s)`, outPathAlpha))
	require.Contains(t, stdout, fmt.Sprintf(`server "zebra": drift detected (%s)`, outPathZebra))
	require.Contains(t, stdout, "+ added: deletepet (DELETE /pet/{petId})")
}

func TestOpenAPIGenerateCheck_Swagger2Skipped(t *testing.T) {
	path := writeSpecFile(t, "legacy.json", swagger2SpecJSON)
	outPath := filepath.Join(t.TempDir(), "legacy.yaml")
	withGlobalConfig(t, &config.Config{
		MCPServer: config.Servers{
			"legacy": &config.Server{
				Spec: path, BaseURL: "http://example.local",
				Tools: &config.ToolsConfig{File: outPath},
			},
		},
	})

	stdout, stderr, err := execOpenAPITools(t, "generate", "--check")
	require.NoError(t, err, "a skipped swagger2 server must not fail the command")
	require.Contains(t, stderr, `server "legacy"`)
	require.Contains(t, stderr, "does not support Swagger 2.x")
	require.NotContains(t, stdout, "legacy")

	_, statErr := os.Stat(outPath)
	require.True(
		t, os.IsNotExist(statErr), "no file should have been touched for a swagger2 server",
	)
}
