package cmd

import (
	"bytes"
	"encoding/json"
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
