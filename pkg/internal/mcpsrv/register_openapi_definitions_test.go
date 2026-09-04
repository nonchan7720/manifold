package mcpsrv

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// inlineDefinitionsSpec は Definitions() の名前・method・path・binaryResponse を
// 検証するための最小限の OpenAPI 3 spec。2 GET + 1 binary レスポンスの POST を持つ。
const inlineDefinitionsSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Inline", "version": "1.0.0"},
  "paths": {
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
    "/pet": {
      "post": {
        "operationId": "addPet",
        "summary": "Add a new pet to the store",
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/pet/{petId}/image": {
      "get": {
        "operationId": "downloadPetImage",
        "summary": "Download the pet's image",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "image/png": {"schema": {"type": "string", "format": "binary"}}
            }
          }
        }
      }
    }
  }
}`

func TestMCPToolRegistry_Definitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "openapi.json")
	require.NoError(t, os.WriteFile(path, []byte(inlineDefinitionsSpec), 0o600))

	r, err := RegisterOpenAPI(t.Context(), path, "", nil)
	require.NoError(t, err)

	defs := r.Definitions()
	require.Len(t, defs, 3)

	// name 昇順でソートされていること: addpet, downloadpetimage, getpetbyid
	require.Equal(t, []string{"addpet", "downloadpetimage", "getpetbyid"}, []string{
		defs[0].Name, defs[1].Name, defs[2].Name,
	})

	want := map[string]ToolDefinition{
		"addpet": {
			Name:           "addpet",
			Method:         "POST",
			Path:           "/pet",
			Description:    "Add a new pet to the store",
			BinaryResponse: false,
		},
		"downloadpetimage": {
			Name:           "downloadpetimage",
			Method:         "GET",
			Path:           "/pet/{petId}/image",
			Description:    "Download the pet's image",
			BinaryResponse: true,
		},
		"getpetbyid": {
			Name:           "getpetbyid",
			Method:         "GET",
			Path:           "/pet/{petId}",
			Description:    "Find pet by ID",
			BinaryResponse: false,
		},
	}
	for _, got := range defs {
		w, ok := want[got.Name]
		require.True(t, ok, "unexpected tool %q", got.Name)
		require.Equal(t, w.Method, got.Method, "method for %s", got.Name)
		require.Equal(t, w.Path, got.Path, "path for %s", got.Name)
		require.Equal(t, w.Description, got.Description, "description for %s", got.Name)
		require.Equal(t, w.BinaryResponse, got.BinaryResponse, "binaryResponse for %s", got.Name)
		require.NotNil(t, got.InputSchema, "inputSchema for %s", got.Name)
		require.Equal(t, "object", got.InputSchema["type"])
	}
}

func TestMCPToolRegistry_Definitions_Swagger2_NotBinary(t *testing.T) {
	r, err := RegisterOpenAPI(t.Context(), "fixtures/petstore_swagger.json", "", nil)
	require.NoError(t, err)

	for _, def := range r.Definitions() {
		require.False(
			t, def.BinaryResponse, "swagger2 tool %q should never be marked binary", def.Name,
		)
		require.NotEmpty(t, def.Method, "method for %s", def.Name)
		require.NotEmpty(t, def.Path, "path for %s", def.Name)
	}
}
